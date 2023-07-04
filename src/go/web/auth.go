package web

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"phenix/util/plog"
	"phenix/web/rbac"
	"phenix/web/util"

	"github.com/dgrijalva/jwt-go"
	"github.com/gorilla/mux"
)

func Signup(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "Signup")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		plog.Error("reading request body", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	var req SignupRequest
	if err := json.Unmarshal(body, &req); err != nil {
		plog.Error("unmashaling request body", "err", err)
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	if o.proxyAuthHeader != "" {
		if user := r.Header.Get(o.proxyAuthHeader); user != req.Username {
			http.Error(w, "proxy user mismatch", http.StatusUnauthorized)
			return
		}
	}

	u := rbac.NewUser(req.Username, req.Password)

	u.Spec.FirstName = req.FirstName
	u.Spec.LastName = req.LastName

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": time.Now().Add(o.jwtLifetime).Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	if err := u.AddToken(signed, time.Now().Format(time.RFC3339)); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: signed,
	}

	body, err = json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func Login(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "Login")

	var (
		user, pass string
		proxied    bool
	)

	switch r.Method {
	case "GET":
		if o.proxyAuthHeader == "" {
			var ok bool

			user, pass, ok = r.BasicAuth()

			if !ok {
				query := r.URL.Query()

				user = query.Get("user")
				if user == "" {
					http.Error(w, "no username provided", http.StatusBadRequest)
					return
				}

				pass = query.Get("pass")
				if pass == "" {
					http.Error(w, "no password provided", http.StatusBadRequest)
					return
				}
			}
		} else {
			user = r.Header.Get(o.proxyAuthHeader)

			if user == "" {
				http.Error(w, "proxy authentication failed", http.StatusUnauthorized)
				return
			}

			proxied = true
		}
	case "POST":
		if o.proxyAuthHeader != "" {
			http.Error(w, "proxy auth enabled -- must login via GET request", http.StatusBadRequest)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "no data provided in POST", http.StatusBadRequest)
			return
		}

		var req LoginRequest
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, "invalid data provided in POST", http.StatusBadRequest)
			return
		}

		if user = req.Username; user == "" {
			http.Error(w, "invalid username provided in POST", http.StatusBadRequest)
			return
		}

		if pass = req.Password; pass == "" {
			http.Error(w, "invalid password provided in POST", http.StatusBadRequest)
			return
		}
	default:
		http.Error(w, "invalid method", http.StatusBadRequest)
		return
	}

	u, err := rbac.GetUser(user)
	if err != nil {
		http.Error(w, user, http.StatusNotFound)
		return
	}

	if !proxied {
		if err := u.ValidatePassword(pass); err != nil {
			http.Error(w, "invalid creds", http.StatusUnauthorized)
			return
		}
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": time.Now().Add(o.jwtLifetime).Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	if err := u.AddToken(signed, time.Now().Format(time.RFC3339)); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := LoginResponse{
		User:  userFromRBAC(*u),
		Token: signed,
	}

	body, err := json.Marshal(resp)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}

func Logout(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "Logout")

	var (
		ctx   = r.Context()
		user  = ctx.Value("user").(string)
		token = ctx.Value("jwt").(string)
	)

	u, err := rbac.GetUser(user)
	if err != nil {
		http.Error(w, "cannot find user", http.StatusBadRequest)
		return
	}

	if err := u.DeleteToken(token); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// POST /users/{username}/tokens
func CreateUserToken(w http.ResponseWriter, r *http.Request) {
	plog.Debug("HTTP handler called", "handler", "CreateUserToken")

	var (
		ctx   = r.Context()
		role  = ctx.Value("role").(rbac.Role)
		vars  = mux.Vars(r)
		uname = vars["username"]
	)

	if !role.Allowed("users", "patch", uname) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}

	u, err := rbac.GetUser(uname)
	if err != nil {
		http.Error(w, "unable to get user", http.StatusInternalServerError)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var req CreateTokenRequest

	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	dur, err := time.ParseDuration(req.Lifetime)
	if err != nil {
		days, err := strconv.Atoi(req.Lifetime)
		if err != nil {
			http.Error(w, "invalid token lifetime provided", http.StatusBadRequest)
			return
		}

		dur = time.Duration(days) * 24 * time.Hour
	}

	exp := time.Now().Add(dur)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": u.Username(),
		"exp": exp.Unix(),
	})

	// Sign and get the complete encoded token as a string using the secret
	signed, err := token.SignedString([]byte(o.jwtKey))
	if err != nil {
		http.Error(w, "failed to sign JWT", http.StatusInternalServerError)
		return
	}

	note := fmt.Sprintf("manually generated - %s", time.Now().Format(time.RFC3339))
	if desc := req.Description; desc != "" {
		note = req.Description
	}

	if err := u.AddToken(signed, note); err != nil {
		http.Error(w, "", http.StatusInternalServerError)
		return
	}

	resp := CreateTokenResponse{
		Token:       signed,
		Description: note,
		Expiration:  exp.Format(time.RFC3339),
	}

	body, _ = json.Marshal(resp)
	w.Write(body)
}

// GET /roles
func GetRoles(w http.ResponseWriter, r *http.Request) {
	plog.Debug("ListRoles HTTP handler called")

	var (
		ctx  = r.Context()
		role = ctx.Value("role").(rbac.Role)
	)

	if !role.Allowed("roles", "list") {
		http.Error(w, "forbidden to list roles", http.StatusForbidden)
		return
	}

	var resp []Role

	roles, err := rbac.GetRoles()
	if err != nil {
		plog.Error("retrieving roles", "err", err)
		http.Error(w, "error retrieving roles", http.StatusInternalServerError)
		return
	}

	for _, r := range roles {
		resp = append(resp, roleFromRBAC(*r))
	}

	body, err := json.Marshal(util.WithRoot("roles", resp))
	if err != nil {
		plog.Error("marshaling roles", "err", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Write(body)
}
