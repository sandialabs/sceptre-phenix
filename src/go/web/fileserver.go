package web

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"time"

	"phenix/api/experiment"
	"phenix/types"
	"phenix/util/plog"
	"phenix/web/middleware"
	"phenix/web/rbac"
)

const maxFileServerUploadMemory = 32 << 20
const fileServerTokenCookie = "phenix_file_server_token"

const fileServerIndexHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>phenix file upload</title>
  <style>
    body { font-family: sans-serif; margin: 2rem; max-width: 42rem; }
    form { display: grid; gap: 1rem; }
    label { display: grid; gap: .35rem; font-weight: 600; }
    select, input, button { font: inherit; padding: .5rem; }
    .message { margin-bottom: 1rem; }
    .upload-progress { display: grid; gap: .35rem; }
    .upload-progress[hidden] { display: none; }
    .upload-error { color: #b00020; }
    progress { width: 100%; }
  </style>
</head>
<body>
  <h1>Upload experiment files</h1>
  {{if and .AuthEnabled .User}}<p>Signed in as {{.User}}. <a href="/logout">Logout</a></p>{{end}}
  {{if .Message}}<p class="message">{{.Message}}</p>{{end}}
  {{if .Experiments}}
  <form id="upload-form" action="/upload" method="post" enctype="multipart/form-data">
    <label>Experiment
      <select name="experiment" required>
        {{range .Experiments}}<option value="{{.}}">{{.}}</option>{{end}}
      </select>
    </label>
    <label>Files
      <input type="file" name="files" multiple required>
    </label>
    <button type="submit">Upload</button>
    <div id="upload-progress" class="upload-progress" hidden>
      <progress id="upload-progress-bar" max="100" value="0"></progress>
      <span id="upload-progress-text">Preparing upload...</span>
    </div>
    <p id="upload-error" class="upload-error" hidden></p>
  </form>
  {{else}}
  <p>No experiments found.</p>
  {{end}}
  <script>
    (function() {
      var form = document.getElementById("upload-form");

      if (!form || !window.XMLHttpRequest || !window.FormData) {
        return;
      }

      var button = form.querySelector("button[type='submit']");
      var progress = document.getElementById("upload-progress");
      var progressBar = document.getElementById("upload-progress-bar");
      var progressText = document.getElementById("upload-progress-text");
      var uploadError = document.getElementById("upload-error");

      form.addEventListener("submit", function(event) {
        event.preventDefault();

        var request = new XMLHttpRequest();
        var data = new FormData(form);

        uploadError.hidden = true;
        uploadError.textContent = "";
        progress.hidden = false;
        progressBar.value = 0;
        progressText.textContent = "Preparing upload...";
        button.disabled = true;

        request.upload.onprogress = function(progressEvent) {
          if (!progressEvent.lengthComputable) {
            progressText.textContent = "Uploading...";

            return;
          }

          var percent = Math.round((progressEvent.loaded / progressEvent.total) * 100);
          progressBar.value = percent;
          progressText.textContent = percent >= 100 ? "Finalizing..." : "Uploading " + percent + "%";
        };

        request.onload = function() {
          if (request.responseURL && new URL(request.responseURL).pathname === "/login") {
            window.location = "/login";

            return;
          }

          if (request.status >= 200 && request.status < 300) {
            document.open();
            document.write(request.responseText);
            document.close();

            return;
          }

          uploadError.textContent = request.responseText || "Upload failed.";
          uploadError.hidden = false;
          button.disabled = false;
        };

        request.onerror = function() {
          uploadError.textContent = "Upload failed.";
          uploadError.hidden = false;
          button.disabled = false;
        };

        request.open(form.method, form.action);
        request.send(data);
      });
    })();
  </script>
</body>
</html>
`

const fileServerLoginHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>phenix file upload login</title>
  <style>
    body { font-family: sans-serif; margin: 2rem; max-width: 24rem; }
    form { display: grid; gap: 1rem; }
    label { display: grid; gap: .35rem; font-weight: 600; }
    input, button { font: inherit; padding: .5rem; }
    .error { color: #b00020; }
  </style>
</head>
<body>
  <h1>Sign in</h1>
  {{if .Error}}<p class="error">{{.Error}}</p>{{end}}
  {{if .Proxy}}
  <p>Proxy authentication is enabled. Use the button below to continue.</p>
  <form action="/login" method="get">
    <button type="submit">Continue</button>
  </form>
  {{else}}
  <form action="/login" method="post">
    <label>Username
      <input type="text" name="username" autocomplete="username" required>
    </label>
    <label>Password
      <input type="password" name="password" autocomplete="current-password" required>
    </label>
    <button type="submit">Sign in</button>
  </form>
  {{end}}
</body>
</html>
`

//nolint:gochecknoglobals // shared parsed template
var fileServerIndexTemplate = template.Must(
	template.New("fileserver-index").Parse(fileServerIndexHTML),
)

//nolint:gochecknoglobals // shared parsed template
var fileServerLoginTemplate = template.Must(
	template.New("fileserver-login").Parse(fileServerLoginHTML),
)

type fileServerIndexData struct {
	User        string
	Message     string
	Experiments []string
	AuthEnabled bool
}

type fileServerLoginData struct {
	Error string
	Proxy bool
}

type fileServerResponseRecorder struct {
	header http.Header
	body   bytes.Buffer
	status int
}

// StartFileServer starts an authenticated experiment file upload server.
func StartFileServer(endpoint string, jwtKey, proxyAuthHeader string) {
	mux := http.NewServeMux()
	authEnabled := jwtKey != ""
	mux.HandleFunc("/", showFileServerIndex(authEnabled))
	mux.HandleFunc("/login", fileServerLogin(proxyAuthHeader))
	mux.HandleFunc("/upload", uploadExperimentFiles(authEnabled))

	plog.Info(plog.TypeSystem, "starting file server", "endpoint", endpoint)
	handler := fileServerCookieAuth(middleware.Auth(jwtKey, proxyAuthHeader)(mux), jwtKey)

	//nolint:gosec // simple opt-in upload server; no timeouts to match UI server style
	if err := http.ListenAndServe(endpoint, handler); err != nil {
		plog.Error(plog.TypeSystem, "serving file server", "err", err)
	}
}

// fileServerCookieAuth bridges fileserver login cookies to phenix auth headers.
func fileServerCookieAuth(h http.Handler, jwtKey string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/logout" {
			clearFileServerTokenCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)

			return
		}

		cookie, err := r.Cookie(fileServerTokenCookie)
		if err == nil && r.Header.Get("X-Phenix-Auth-Token") == "" {
			token, err := decodeFileServerTokenCookie(cookie.Value)
			if err != nil {
				plog.Warn(plog.TypeSecurity, "decoding file server auth cookie", "err", err)
				clearFileServerTokenCookie(w)
				http.Redirect(w, r, "/login", http.StatusSeeOther)

				return
			}

			r.Header.Set("X-Phenix-Auth-Token", "Bearer "+token)
		}

		if jwtKey != "" && r.URL.Path != "/login" && r.Header.Get("X-Phenix-Auth-Token") == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)

			return
		}

		rec := newFileServerResponseRecorder()
		h.ServeHTTP(rec, r)
		if rec.status == http.StatusUnauthorized {
			clearFileServerTokenCookie(w)
			http.Redirect(w, r, "/login", http.StatusSeeOther)

			return
		}

		rec.writeTo(w)
	})
}

// newFileServerResponseRecorder buffers fileserver responses so auth errors can redirect.
func newFileServerResponseRecorder() *fileServerResponseRecorder {
	return &fileServerResponseRecorder{header: make(http.Header)} //nolint:exhaustruct // partial initialization
}

// Header returns the buffered response headers.
func (r *fileServerResponseRecorder) Header() http.Header {
	return r.header
}

// WriteHeader records the response status code.
func (r *fileServerResponseRecorder) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

// Write records the response body.
func (r *fileServerResponseRecorder) Write(data []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}

	return r.body.Write(data)
}

// writeTo copies the buffered response to the real response writer.
func (r *fileServerResponseRecorder) writeTo(w http.ResponseWriter) {
	for key, values := range r.header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	if r.status == 0 {
		r.status = http.StatusOK
	}

	w.WriteHeader(r.status)
	_, _ = w.Write(r.body.Bytes())
}

// showFileServerIndex renders the experiment file upload form.
func showFileServerIndex(authEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)

			return
		}

		renderFileServerIndex(w, r, "", authEnabled)
	}
}

// fileServerLogin renders login and sets the fileserver auth cookie.
func fileServerLogin(proxyAuthHeader string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if proxyAuthHeader == "" && r.Header.Get("X-Phenix-Auth-Token") == "" {
				renderFileServerLogin(w, fileServerLoginData{Error: "", Proxy: false})

				return
			}

			token, err := loginFileServerUser(r, nil)
			if err != nil {
				renderFileServerLogin(w, fileServerLoginData{Error: err.Error(), Proxy: proxyAuthHeader != ""})

				return
			}

			setFileServerTokenCookie(w, token)
			http.Redirect(w, r, "/", http.StatusSeeOther)
		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				renderFileServerLogin(w, fileServerLoginData{Error: err.Error(), Proxy: proxyAuthHeader != ""})

				return
			}

			req := LoginRequest{Username: r.FormValue("username"), Password: r.FormValue("password")}
			token, err := loginFileServerUser(r, &req)
			if err != nil {
				renderFileServerLogin(w, fileServerLoginData{Error: err.Error(), Proxy: proxyAuthHeader != ""})

				return
			}

			setFileServerTokenCookie(w, token)
			http.Redirect(w, r, "/", http.StatusSeeOther)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// loginFileServerUser calls the built-in phenix login handler and returns its token.
func loginFileServerUser(r *http.Request, req *LoginRequest) (string, error) {
	var body io.Reader
	method := http.MethodGet

	if req != nil {
		data, err := json.Marshal(req)
		if err != nil {
			return "", fmt.Errorf("building login request: %w", err)
		}

		body = bytes.NewReader(data)
		method = http.MethodPost
	}

	loginReq := httptest.NewRequest(method, "/login", body)
	loginReq = loginReq.WithContext(r.Context())
	loginReq.Header = r.Header.Clone()

	if req != nil {
		loginReq.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	Login(rec, loginReq)

	if rec.Code < http.StatusOK || rec.Code >= http.StatusMultipleChoices {
		return "", fmt.Errorf("%s", rec.Body.String())
	}

	var resp LoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", fmt.Errorf("parsing login response: %w", err)
	}

	if resp.Token == "" {
		return "", errors.New("missing login token")
	}

	return resp.Token, nil
}

// setFileServerTokenCookie stores the phenix auth token for browser form requests.
func setFileServerTokenCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     fileServerTokenCookie,
		Value:    encodeFileServerTokenCookie(token),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// encodeFileServerTokenCookie makes the JWT safe for browser cookie transport.
func encodeFileServerTokenCookie(token string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

// decodeFileServerTokenCookie restores the JWT stored in the fileserver auth cookie.
func decodeFileServerTokenCookie(value string) (string, error) {
	token, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return "", err
	}

	return string(token), nil
}

// clearFileServerTokenCookie expires the fileserver auth cookie.
func clearFileServerTokenCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     fileServerTokenCookie,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// uploadExperimentFiles stores posted files under the selected experiment files directory.
func uploadExperimentFiles(authEnabled bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

			return
		}

		if err := r.ParseMultipartForm(maxFileServerUploadMemory); err != nil {
			http.Error(w, "parsing upload: "+err.Error(), http.StatusBadRequest)

			return
		}

		exp, err := experiment.Get(r.FormValue("experiment"))
		if err != nil {
			http.Error(w, "getting experiment: "+err.Error(), http.StatusBadRequest)

			return
		}

		role := middleware.RoleFromContext(r.Context())
		if !role.Allowed("experiments/files", "create", exp.Metadata.Name) {
			http.Error(w, "forbidden", http.StatusForbidden)

			return
		}

		files := r.MultipartForm.File["files"]
		if len(files) == 0 {
			http.Error(w, "no files uploaded", http.StatusBadRequest)

			return
		}

		dir := exp.FilesDir()
		if err := os.MkdirAll(dir, 0o750); err != nil {
			http.Error(w, "creating experiment files directory: "+err.Error(), http.StatusInternalServerError)

			return
		}

		var uploaded int
		for _, header := range files {
			name := filepath.Base(header.Filename)
			if name == "" || name == "." {
				http.Error(w, "invalid upload filename", http.StatusBadRequest)

				return
			}

			clientFile, err := header.Open()
			if err != nil {
				http.Error(w, "opening upload: "+err.Error(), http.StatusInternalServerError)

				return
			}

			target, err := os.OpenFile(filepath.Join(dir, name), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
			if err != nil {
				_ = clientFile.Close()
				http.Error(w, "creating target file: "+err.Error(), http.StatusInternalServerError)

				return
			}

			_, copyErr := io.Copy(target, clientFile)
			closeClientErr := clientFile.Close()
			closeTargetErr := target.Close()
			if copyErr != nil {
				http.Error(w, "saving upload: "+copyErr.Error(), http.StatusInternalServerError)

				return
			}
			if closeClientErr != nil {
				http.Error(w, "closing upload: "+closeClientErr.Error(), http.StatusInternalServerError)

				return
			}
			if closeTargetErr != nil {
				http.Error(w, "closing target file: "+closeTargetErr.Error(), http.StatusInternalServerError)

				return
			}

			uploaded++
		}

		renderFileServerIndex(w, r, fmt.Sprintf("Uploaded %d file(s) to %s.", uploaded, exp.Metadata.Name), authEnabled)
	}
}

// renderFileServerIndex writes the upload page with current experiment names.
func renderFileServerIndex(w http.ResponseWriter, r *http.Request, message string, authEnabled bool) {
	experiments, err := experiment.List()
	if err != nil {
		http.Error(w, "listing experiments: "+err.Error(), http.StatusInternalServerError)

		return
	}

	role := middleware.RoleFromContext(r.Context())
	names := experimentNames(experiments, role)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	data := fileServerIndexData{
		User:        middleware.UserFromContext(r.Context()),
		Message:     message,
		Experiments: names,
		AuthEnabled: authEnabled,
	}
	if err := fileServerIndexTemplate.Execute(w, data); err != nil {
		plog.Error(plog.TypeSystem, "rendering file server index", "err", err)
	}
}

// renderFileServerLogin writes the fileserver login page.
func renderFileServerLogin(w http.ResponseWriter, data fileServerLoginData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if err := fileServerLoginTemplate.Execute(w, data); err != nil {
		plog.Error(plog.TypeSystem, "rendering file server login", "err", err)
	}
}

// experimentNames returns sorted experiment names the user can upload to.
func experimentNames(experiments []types.Experiment, role rbac.Role) []string {
	names := make([]string, 0, len(experiments))
	for _, exp := range experiments {
		if !role.Allowed("experiments/files", "create", exp.Metadata.Name) {
			continue
		}

		names = append(names, exp.Metadata.Name)
	}

	sort.Strings(names)

	return names
}
