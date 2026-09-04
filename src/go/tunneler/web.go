package main

import (
	"bytes"
	"embed"
	"errors"
	"html/template"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/net/websocket"
)

//go:embed web/index.html
var webTemplateFS embed.FS

type webPageData struct {
	Listeners Listeners
	Origin    string
	Message   string
	Error     string
}

type webEventHub struct {
	mu       sync.Mutex
	clients  map[*webEventClient]struct{}
	template *template.Template
}

type webEventClient struct {
	conn     *websocket.Conn
	messages chan string
}

var webEvents = &webEventHub{clients: make(map[*webEventClient]struct{})} //nolint:gochecknoglobals // local web server state

func (h *webEventHub) setTemplate(tmpl *template.Template) {
	h.mu.Lock()
	h.template = tmpl
	h.mu.Unlock()
}

func (h *webEventHub) add(conn *websocket.Conn) *webEventClient {
	client := &webEventClient{conn: conn, messages: make(chan string, 1)} //nolint:exhaustruct // initialized below

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	client.sendListeners(localListeners.snapshot())

	return client
}

func (h *webEventHub) remove(client *webEventClient) {
	h.mu.Lock()
	delete(h.clients, client)
	h.mu.Unlock()
}

func (h *webEventHub) broadcast(listeners Listeners) {
	h.mu.Lock()

	var (
		tmpl    = h.template
		clients = make([]*webEventClient, 0, len(h.clients))
	)

	for client := range h.clients {
		clients = append(clients, client)
	}

	h.mu.Unlock()

	if tmpl == nil {
		return
	}

	payload, err := renderWebListeners(tmpl, listeners)
	if err != nil {
		return
	}

	for _, client := range clients {
		client.send(string(payload))
	}
}

func (c *webEventClient) sendListeners(listeners Listeners) {
	webEvents.mu.Lock()
	tmpl := webEvents.template
	webEvents.mu.Unlock()

	if tmpl == nil {
		return
	}

	payload, err := renderWebListeners(tmpl, listeners)
	if err == nil {
		c.send(string(payload))
	}
}

func (c *webEventClient) send(payload string) {
	select {
	case c.messages <- payload:
	default:
		<-c.messages
		c.messages <- payload
	}
}

func broadcastWebListeners() {
	webEvents.broadcast(localListeners.snapshot())
}

func startWebServer(addr string) error {
	handler, err := newWebHandler()
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", addr) //nolint:gosec // address is configured by the local user
	if err != nil {
		return err
	}

	server := &http.Server{Handler: handler} //nolint:exhaustruct // defaults are intentional
	go server.Serve(listener)

	return nil
}

func newWebHandler() (http.Handler, error) {
	tmpl, err := template.ParseFS(webTemplateFS, "web/index.html")
	if err != nil {
		return nil, err
	}

	webEvents.setTemplate(tmpl)

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}

		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)

			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		renderWebPage(w, tmpl, webPageData{
			Listeners: localListeners.snapshot(),
			Origin:    origin,
		})
	})

	mux.HandleFunc("/listeners/", func(w http.ResponseWriter, r *http.Request) {
		handleWebListenerAction(w, r, tmpl)
	})

	mux.Handle("/events", websocket.Handler(func(conn *websocket.Conn) {
		client := webEvents.add(conn)
		defer webEvents.remove(client)

		for payload := range client.messages {
			if err := websocket.Message.Send(conn, payload); err != nil {
				return
			}
		}
	}))

	return mux, nil
}

func handleWebListenerAction(w http.ResponseWriter, r *http.Request, tmpl *template.Template) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)

		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")

	if len(parts) != 3 {
		http.NotFound(w, r)
		return
	}

	id, err := strconv.Atoi(parts[1])
	if err != nil {
		renderWebError(w, tmpl, http.StatusBadRequest, "malformed listener ID")
		return
	}

	if err := validateListenerID(id); err != nil {
		renderWebError(w, tmpl, http.StatusBadRequest, err.Error())
		return
	}

	var action func(*LocalListener) error
	switch parts[2] {
	case "activate":
		action = activateLocalListener
	case "deactivate":
		action = deactivateLocalListener
	case "move":
		if err := r.ParseForm(); err != nil {
			renderWebError(w, tmpl, http.StatusBadRequest, "parsing form: "+err.Error())
			return
		}

		port, err := parseLocalPort(r.FormValue("port"))
		if err != nil {
			renderWebError(w, tmpl, http.StatusBadRequest, err.Error())
			return
		}

		action = func(listener *LocalListener) error {
			return moveLocalListener(listener, port)
		}
	default:
		http.NotFound(w, r)
		return
	}

	err = localListeners.withListener(id, action)
	if errors.Is(err, errListenerNotFound) {
		renderWebError(w, tmpl, http.StatusNotFound, "listener "+strconv.Itoa(id)+" not found")
		return
	}

	if err != nil {
		renderWebError(w, tmpl, http.StatusBadRequest, err.Error())
		return
	}

	broadcastWebListeners()
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func renderWebPage(w http.ResponseWriter, tmpl *template.Template, data webPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.Execute(w, data); err != nil {
		http.Error(w, "unable to render page", http.StatusInternalServerError)
	}
}

func renderWebListeners(tmpl *template.Template, listeners Listeners) ([]byte, error) {
	var body bytes.Buffer

	if err := tmpl.ExecuteTemplate(&body, "listenerRows", listeners); err != nil {
		return nil, err
	}

	return body.Bytes(), nil
}

func renderWebError(w http.ResponseWriter, tmpl *template.Template, status int, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	renderWebPage(w, tmpl, webPageData{
		Listeners: localListeners.snapshot(),
		Origin:    origin,
		Error:     message,
	})
}
