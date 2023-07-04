package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"

	"phenix/version"

	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"

	"github.com/dgrijalva/jwt-go"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/net/websocket"
	"golang.org/x/term"
)

var (
	// used by server for websocket connections
	wsEndpoint, origin string

	httpCli = new(http.Client)
	headers = make(http.Header)

	listenerIDs = make(chan int)
	// key will be "<exp>:<vm>:<fwd host>:<dst port>"
	listeners = make(map[string]*LocalListener)

	username string
)

var rootCmd = &cobra.Command{
	Use:     "phenix-tunneler",
	Version: version.Commit,

	Long: `A TCP traffic tunneler for phēnix VMs

Create local TCP port listeners for VM port forwards created in the phēnix UI.

Start the tunneler locally with the 'serve' subcommand. If the phēnix UI being
connected to has authentication enabled, a username can be provided to the
'serve' command and it will prompt for a password. An API token can also be
provided to bypass manual login.

Listeners for port forwards created in the phēnix UI are automatically created
locally if created by the same user. Local listeners for port forwards created
by other users can be created manually using the 'activate' subcommand.
	`,

	RunE: func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	},

	PersistentPostRun: func(cmd *cobra.Command, args []string) {
		fmt.Println()
	},

	SilenceUsage: true, // don't print help when subcommands return an error
}

var serveCmd = &cobra.Command{
	Use:   "serve <url>",
	Short: "Start local WebSocket proxy server",

	RunE: func(cmd *cobra.Command, args []string) error {
		origin = args[0]

		username, err := cmd.Flags().GetString("username")
		if err != nil {
			return fmt.Errorf("unable to get --username flag")
		}

		token, err := cmd.Flags().GetString("auth-token")
		if err != nil {
			return fmt.Errorf("unable to get --auth-token flag")
		}

		u, err := url.Parse(origin)
		if err != nil {
			return fmt.Errorf("parsing URL: %w", err)
		}

		if token != "" {
			var claims jwt.MapClaims

			_, _, err := new(jwt.Parser).ParseUnverified(token, &claims)
			if err != nil {
				return fmt.Errorf("parsing phenix auth token for username: %w", err)
			}

			sub, ok := claims["sub"].(string)
			if !ok {
				return fmt.Errorf("username missing from phenix auth token")
			}

			if username != "" && sub != username {
				return fmt.Errorf("provided username does not match token subject")
			}

			headers.Set("X-phenix-auth-token", "Bearer "+token)
		} else if username != "" {
			fmt.Printf("Password for %s: ", username)

			prev, err := term.MakeRaw(0)
			if err != nil {
				return fmt.Errorf("unable to put terminal into raw mode for hiding password: %w", err)
			}

			terminal := term.NewTerminal(os.Stdin, "")

			passwd, err := terminal.ReadPassword("")
			if err != nil {
				return fmt.Errorf("unable to read password entered by user: %w", err)
			}

			if err := term.Restore(0, prev); err != nil {
				return fmt.Errorf("unable to restore terminal mode to previous state: %w", err)
			}

			var (
				auth  = base64.StdEncoding.EncodeToString([]byte(username + ":" + passwd))
				login = origin + "/api/v1/login"
			)

			req, err := http.NewRequest(http.MethodGet, login, nil)
			if err != nil {
				return fmt.Errorf("creating login request: %w", err)
			}

			req.Header.Set("Authorization", "Basic "+auth)
			headers.Set("Authorization", "Basic "+auth)

			resp, err := httpCli.Do(req)
			if err != nil {
				return fmt.Errorf("making login request: %w", err)
			}

			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("login failed (%d)", resp.StatusCode)
			}

			body, err := io.ReadAll(resp.Body)
			if err != nil {
				return fmt.Errorf("reading login response: %w", err)
			}

			var user map[string]any

			if err := json.Unmarshal(body, &user); err != nil {
				return fmt.Errorf("parsing login response: %w", err)
			}

			headers.Set("X-phenix-auth-token", "Bearer "+user["token"].(string))
		}

		if username != "" {
			fmt.Printf("phēnix user: %s\n", username)
		}

		switch u.Scheme {
		case "http":
			wsEndpoint = "ws://"
		case "https":
			wsEndpoint = "wss://"
		}

		wsEndpoint += u.Host + u.Path

		wsURL := wsEndpoint + "/api/v1/ws"

		httpCli = &http.Client{
			Transport: AddHeaderTransport{
				T:       http.DefaultTransport,
				Headers: headers,
			},
		}

		config, err := websocket.NewConfig(wsURL, origin)
		if err != nil {
			return fmt.Errorf("creating websocket config: %w", err)
		}

		config.Header = headers

		ws, err := websocket.DialConfig(config)
		if err != nil {
			return fmt.Errorf("dialing websocket (%s): %w", wsURL, err)
		}

		go func() { // start a goroutine to generate listener IDs
			for id := 1; ; id++ {
				listenerIDs <- id
			}
		}()

		existing, err := getRemoteListeners()
		if err != nil {
			return fmt.Errorf("getting initial list of existing listeners: %w", err)
		}

		for _, listener := range existing {
			if err := createLocalListener(listener); err != nil {
				fmt.Printf("ERROR: creating local listener: %v\n", err)
			}
		}

		if err := startUnixSocket(); err != nil {
			return fmt.Errorf("starting unix socket: %w", err)
		}

		for {
			var publish bt.Publish
			if err := websocket.JSON.Receive(ws, &publish); err != nil {
				if errors.Is(err, io.EOF) {
					return fmt.Errorf("phēnix connection terminated")
				}

				continue
			}

			if publish.Resource.Type == "experiment/vm/forward" {
				switch publish.Resource.Action {
				case "create":
					var listener ft.Listener
					if err := json.Unmarshal(publish.Result, &listener); err != nil {
						fmt.Printf("ERROR: parsing forward create: %v\n", err)
						continue
					}

					if err := createLocalListener(listener); err != nil {
						fmt.Printf("ERROR: creating local listener: %v\n", err)
					}
				case "delete":
					var payload map[string]string
					if err := json.Unmarshal(publish.Result, &payload); err != nil {
						fmt.Printf("ERROR: parsing forward delete: %v\n", err)
						continue
					}

					if _, ok := listeners[payload["key"]]; ok {
						deleteLocalListener(payload["key"])
					}
				}
			}
		}
	},
}

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Show table of known port forwards",

	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("ERROR: creating new client: %w", err)
		}

		defer cli.close()

		listeners, err := cli.getLocalListeners()
		if err != nil {
			return fmt.Errorf("ERROR: getting list of listeners: %w", err)
		}

		if len(listeners) == 0 {
			fmt.Println("No registered listeners")
			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader([]string{"ID", "Experiment", "VM", "Remote Host", "Remote Port", "Local Port", "Active"})

		for _, listener := range listeners {
			table.Append([]string{
				fmt.Sprintf("%d", listener.ID),
				listener.Exp,
				listener.VM,
				listener.DstHost,
				fmt.Sprintf("%d", listener.DstPort),
				fmt.Sprintf("%d", listener.SrcPort),
				fmt.Sprintf("%t", listener.Listening),
			})
		}

		table.Render()

		return nil
	},
}

var moveCmd = &cobra.Command{
	Use:   "move <id> <port>",
	Short: "Move listener to a different local port",

	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("malformed listener ID provided (%s): %w", args[0], err)
		}

		port, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("malformed listener port provided (%s): %w", args[1], err)
		}

		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("creating new client: %w", err)
		}

		defer cli.close()

		if err := cli.moveLocalListener(id, port); err != nil {
			return fmt.Errorf("moving listener %d to port %d: %w", id, port, err)
		}

		fmt.Printf("Listener %d moved to port %d\n", id, port)

		return nil
	},
}

var activateCmd = &cobra.Command{
	Use:   "activate <id>",
	Short: "Activate a local forward (start listening on local port)",

	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("malformed listener ID provided (%s): %w", args[0], err)
		}

		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("creating new client: %w", err)
		}

		defer cli.close()

		if err := cli.activateLocalListener(id); err != nil {
			return fmt.Errorf("activating listener %d: %w", id, err)
		}

		fmt.Printf("Listener %d activated\n", id)

		return nil
	},
}

var deactivateCmd = &cobra.Command{
	Use:   "deactivate <id>",
	Short: "Dectivate a local forward (stop listening on local port)",

	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("malformed listener ID provided (%s): %w", args[0], err)
		}

		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("ERROR: creating new client: %w", err)
		}

		defer cli.close()

		if err := cli.deactivateLocalListener(id); err != nil {
			return fmt.Errorf("ERROR: deactivating listener %d: %w", id, err)
		}

		fmt.Printf("Listener %d deactivated\n", id)

		return nil
	},
}

func main() {
	serveCmd.Flags().StringP("username", "u", "", "username to log into phēnix with")
	serveCmd.Flags().StringP("auth-token", "t", "", "phēnix API token (skip login process)")

	rootCmd.AddCommand(listCmd, activateCmd, deactivateCmd, moveCmd, serveCmd)

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
