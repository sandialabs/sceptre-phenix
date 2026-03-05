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

	"github.com/dgrijalva/jwt-go"
	"github.com/olekukonko/tablewriter"
	"github.com/spf13/cobra"
	"golang.org/x/net/websocket"
	"golang.org/x/term"

	"phenix/version"
	bt "phenix/web/broker/brokertypes"
	ft "phenix/web/forward/forwardtypes"
	jwtutil "phenix/web/util/jwt"
)

var (
	// used by server for websocket connections.
	wsEndpoint, origin string //nolint:gochecknoglobals // global state

	httpCli = new(http.Client)  //nolint:gochecknoglobals // global state
	headers = make(http.Header) //nolint:gochecknoglobals // global state

	listenerIDs = make(chan int) //nolint:gochecknoglobals // global state
	// key will be "<exp>:<vm>:<fwd host>:<dst port>".
	listeners = make(map[string]*LocalListener) //nolint:gochecknoglobals // global state

	username string //nolint:gochecknoglobals // global state
)

var rootCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
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
		fmt.Println() //nolint:forbidigo // CLI output
	},

	SilenceUsage: true, // don't print help when subcommands return an error
}

var serveCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
	Use:   "serve <url>",
	Short: "Start local WebSocket proxy server",

	RunE: func(cmd *cobra.Command, args []string) error {
		origin = args[0]

		var err error

		username, err = cmd.Flags().GetString("username")
		if err != nil {
			return errors.New("unable to get --username flag")
		}

		token, err := cmd.Flags().GetString("auth-token")
		if err != nil {
			return errors.New("unable to get --auth-token flag")
		}

		u, err := url.Parse(origin)
		if err != nil {
			return fmt.Errorf("parsing URL: %w", err)
		}

		if token != "" {
			cookie, err := cmd.Flags().GetString("use-cookie")
			if err != nil {
				return errors.New("unable to get --use-cookie flag")
			}

			token, _, err := new(jwt.Parser).ParseUnverified(token, jwt.MapClaims{})
			if err != nil {
				return fmt.Errorf("parsing phenix auth token for username: %w", err)
			}

			claims, _ := token.Claims.(jwt.MapClaims)

			username, err = jwtutil.UsernameFromClaims(claims)
			if err != nil {
				return errors.New("username missing from token")
			}

			if err := jwtutil.ValidateExpirationClaim(claims); err != nil {
				return fmt.Errorf("validating token expiration: %w", err)
			}

			headers.Set("X-Phenix-Auth-Token", "Bearer "+token.Raw)

			if cookie != "" {
				headers.Set("Cookie", fmt.Sprintf("%s=%s", cookie, token.Raw))
			}
		} else if username != "" {
			fmt.Printf("Password for %s: ", username) //nolint:forbidigo // CLI output

			prev, err := term.MakeRaw(0)
			if err != nil {
				return fmt.Errorf(
					"unable to put terminal into raw mode for hiding password: %w",
					err,
				)
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

			tokenStr, _ := user["token"].(string)
			headers.Set("X-Phenix-Auth-Token", "Bearer "+tokenStr)
		}

		if username != "" {
			fmt.Printf("phēnix user: %s\n", username) //nolint:forbidigo // CLI output
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
			err := createLocalListener(listener)
			if err != nil {
				fmt.Printf("err: creating local listener: %v\n", err) //nolint:forbidigo // CLI output
			}
		}

		if err := startUnixSocket(); err != nil {
			return fmt.Errorf("starting unix socket: %w", err)
		}

		for {
			var publish bt.Publish

			err := websocket.JSON.Receive(ws, &publish)
			if err != nil {
				if errors.Is(err, io.EOF) {
					return errors.New("phēnix connection terminated")
				}

				continue
			}

			if publish.Resource.Type == "experiment/vm/forward" {
				switch publish.Resource.Action {
				case "create":
					var listener ft.Listener

					err := json.Unmarshal(publish.Result, &listener)
					if err != nil {
						fmt.Printf("err: parsing forward create: %v\n", err) //nolint:forbidigo // CLI output

						continue
					}

					err = createLocalListener(listener)
					if err != nil {
						fmt.Printf("err: creating local listener: %v\n", err) //nolint:forbidigo // CLI output
					}
				case "delete":
					var payload map[string]string

					err := json.Unmarshal(publish.Result, &payload)
					if err != nil {
						fmt.Printf("err: parsing forward delete: %v\n", err) //nolint:forbidigo // CLI output

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

var listCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
	Use:   "list",
	Short: "Show table of known port forwards",

	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("err: creating new client: %w", err)
		}

		defer func() { _ = cli.close() }()

		listeners, err := cli.getLocalListeners()
		if err != nil {
			return fmt.Errorf("err: getting list of listeners: %w", err)
		}

		if len(listeners) == 0 {
			fmt.Println("No registered listeners") //nolint:forbidigo // CLI output

			return nil
		}

		table := tablewriter.NewWriter(os.Stdout)
		table.SetHeader(
			[]string{
				"ID",
				"Experiment",
				"VM",
				"Remote Host",
				"Remote Port",
				"Local Port",
				"Active",
			},
		)

		for _, listener := range listeners {
			table.Append([]string{
				strconv.Itoa(listener.ID),
				listener.Exp,
				listener.VM,
				listener.DstHost,
				strconv.Itoa(listener.DstPort),
				strconv.Itoa(listener.SrcPort),
				strconv.FormatBool(listener.Listening),
			})
		}

		table.Render()

		return nil
	},
}

var moveCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
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

		defer func() { _ = cli.close() }()

		if err := cli.moveLocalListener(id, port); err != nil {
			return fmt.Errorf("moving listener %d to port %d: %w", id, port, err)
		}

		fmt.Printf("Listener %d moved to port %d\n", id, port) //nolint:forbidigo // CLI output

		return nil
	},
}

var activateCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
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

		defer func() { _ = cli.close() }()

		if err := cli.activateLocalListener(id); err != nil {
			return fmt.Errorf("activating listener %d: %w", id, err)
		}

		fmt.Printf("Listener %d activated\n", id) //nolint:forbidigo // CLI output

		return nil
	},
}

var deactivateCmd = &cobra.Command{ //nolint:gochecknoglobals // cobra command
	Use:   "deactivate <id>",
	Short: "Dectivate a local forward (stop listening on local port)",

	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("malformed listener ID provided (%s): %w", args[0], err)
		}

		cli, err := newClient()
		if err != nil {
			return fmt.Errorf("err: creating new client: %w", err)
		}

		defer func() { _ = cli.close() }()

		if err := cli.deactivateLocalListener(id); err != nil {
			return fmt.Errorf("err: deactivating listener %d: %w", id, err)
		}

		fmt.Printf("Listener %d deactivated\n", id) //nolint:forbidigo // CLI output

		return nil
	},
}

func main() {
	serveCmd.Flags().StringP("username", "u", "", "username to log into phēnix with")
	serveCmd.Flags().StringP("auth-token", "t", "", "phēnix API token (skip login process)")
	serveCmd.Flags().StringP("use-cookie", "c", "", "name of cookie to use for auth token")

	rootCmd.AddCommand(listCmd, activateCmd, deactivateCmd, moveCmd, serveCmd)

	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}
