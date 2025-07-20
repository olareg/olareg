package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gopkg.in/yaml.v3"

	"github.com/olareg/olareg/internal/sloghandle"
	"github.com/olareg/olareg/types"
)

type AuthAccess int

const (
	AuthUnknown AuthAccess = iota
	AuthRead
	AuthWrite
	AuthDelete
	AuthAll // wildcard match
)

const (
	pollFreq    = time.Second * 5
	tokenExpire = time.Minute * 5
)

// MarshalText converts AuthAccess to a string.
func (t AuthAccess) MarshalText() ([]byte, error) {
	var s string
	switch t {
	default:
		s = ""
	case AuthRead:
		s = "read"
	case AuthWrite:
		s = "write"
	case AuthDelete:
		s = "delete"
	case AuthAll:
		s = "*"
	}
	return []byte(s), nil
}

// UnmarshalText converts AuthAccess from a string.
func (t *AuthAccess) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	default:
		return fmt.Errorf("unknown TLS value \"%s\"", b)
	case "":
		*t = AuthUnknown
	case "read":
		*t = AuthRead
	case "write":
		*t = AuthWrite
	case "delete":
		*t = AuthDelete
	case "*":
		*t = AuthAll
	}
	return nil
}

// AuthHandler creates a new [http.Handler] that checks a request for the needed access.
// It may refuse the request with a 401 status, www-authenticate header, and then return.
// It may allow the request by calling the next handler without modifying the request or response writer.
type AuthHandler func(repo string, access AuthAccess, next http.Handler) http.Handler

type authController struct {
	filename   string
	mod, poll  time.Time
	mu         sync.RWMutex
	conf       *authConf
	realm      string
	service    string
	slog       *slog.Logger
	opaque     map[string]authTokenOpaque
	cleanupJob chan struct{}
}

type authConf struct {
	Users  map[string]*authUser  `yaml:"users"`
	Groups map[string]*authGroup `yaml:"groups"`
	ACLs   []authACL             `yaml:"acls"`
}

type authUser struct {
	Cred   string `yaml:"cred"`
	groups map[string]bool
}

type authGroup struct {
	Members []string `yaml:"members"`
}

type authACL struct {
	Repo      string       `yaml:"repo"`
	Access    []AuthAccess `yaml:"access"`
	Members   []string     `yaml:"members"`
	Anonymous bool         `yaml:"anonymous"`
}

type authTokenOpaque struct {
	user    string
	expires time.Time
}

type authTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresIn int64     `json:"expires_in"`
	IssuedAt  time.Time `json:"issued_at"`
}

type AuthOpt func(*authController)

func WithAuthRealm(realm string) AuthOpt {
	return func(af *authController) {
		af.realm = realm
	}
}

func WithAuthService(service string) AuthOpt {
	return func(af *authController) {
		af.service = service
	}
}

func WithAuthSlog(logger *slog.Logger) AuthOpt {
	return func(af *authController) {
		af.slog = logger
	}
}

// newAuthController returns an auth controller with an empty conf, optionally backed by a file.
func newAuthController(filename string) (*authController, error) {
	conf := newAuthConf()
	ac := authController{
		filename:   filename,
		conf:       &conf,
		slog:       slog.New(sloghandle.Discard),
		opaque:     map[string]authTokenOpaque{},
		cleanupJob: make(chan struct{}, 1),
	}
	// validate filename if provided
	if filename != "" {
		_, err := os.Stat(filename)
		if err != nil {
			return &ac, fmt.Errorf("%w: %w", types.ErrNotFound, err)
		}
	}
	return &ac, nil
}

func newAuthConf() authConf {
	return authConf{
		Users:  map[string]*authUser{},
		Groups: map[string]*authGroup{},
		ACLs:   []authACL{},
	}
}

// getConf updates conf from underlying file if it has changed, and returns the current conf.
// The file is only checked every pollFreq, and only if a filename is provided.
// Any errors in reading or parsing the file fall back to the last conf, or empty conf if file was never successfully loaded.
func (ac *authController) getConf() (*authConf, error) {
	if ac.filename == "" {
		return ac.conf, nil
	}
	ac.mu.RLock()
	if time.Since(ac.poll) < pollFreq {
		ret := ac.conf
		ac.mu.RUnlock()
		return ret, nil
	}
	ac.mu.RUnlock()
	ac.mu.Lock()
	defer ac.mu.Unlock()
	// check if another poll ran while waiting for the write lock
	if time.Since(ac.poll) < pollFreq {
		return ac.conf, nil
	}
	ac.poll = time.Now()
	fi, err := os.Stat(ac.filename)
	if err != nil {
		return ac.conf, fmt.Errorf("%w: %w", types.ErrNotFound, err)
	}
	if ac.mod.Equal(fi.ModTime()) {
		return ac.conf, nil
	}
	ac.mod = fi.ModTime()
	fh, err := os.Open(ac.filename)
	if err != nil {
		return ac.conf, err
	}
	afc := newAuthConf()
	if err := yaml.NewDecoder(fh).Decode(&afc); err != nil && !errors.Is(err, io.EOF) {
		return ac.conf, fmt.Errorf("%w: %w", types.ErrParsingFailed, err)
	}
	for group := range afc.Groups {
		afc.groupMap(group)
	}
	ac.conf = &afc
	return ac.conf, nil
}

func (ac *authController) basicHandler(repo string, access AuthAccess, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ac == nil {
			w.WriteHeader(http.StatusInternalServerError)
			ac.slog.Error("auth file is nil", "filename", ac.filename)
			return
		}
		conf, err := ac.getConf()
		if err != nil {
			ac.slog.Warn("failed to refresh auth file", "filename", ac.filename, "err", err.Error())
		}
		if conf == nil {
			ac.slog.Error("auth conf is nil", "filename", ac.filename)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		scope := "repository:" + repo
		if repo == "" {
			scope = "registry:"
		}
		switch access {
		case AuthRead:
			scope += ":pull"
		case AuthWrite:
			scope += ":push"
		case AuthDelete:
			scope += ":delete"
		}
		user, passwd, ok := r.BasicAuth()
		if !ok {
			user = ""
		}
		if ok && (user != "" || passwd != "") && !conf.checkCreds(user, passwd) {
			err := unauthorized(w, "Basic", ac.realm, "", scope, "invalid username or password")
			if err != nil {
				ac.slog.Error("failed to generate unauthorized header", "err", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		if !conf.checkAccess(repo, access, user) {
			err := unauthorized(w, "Basic", ac.realm, "", scope, "unauthorized")
			if err != nil {
				ac.slog.Error("failed to generate unauthorized header", "err", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

func (ac *authController) tokenOpaqueHandler(repo string, access AuthAccess, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if ac == nil {
			w.WriteHeader(http.StatusInternalServerError)
			ac.slog.Error("auth file is nil", "filename", ac.filename)
			return
		}
		conf, err := ac.getConf()
		if err != nil {
			ac.slog.Warn("failed to refresh auth file", "filename", ac.filename, "err", err.Error())
		}
		if conf == nil {
			ac.slog.Error("auth conf is nil", "filename", ac.filename)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		realmURL, err := r.URL.Parse(ac.realm)
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			ac.slog.Error("auth realm cannot be parsed into a url", "realm", ac.realm, "err", err.Error())
			return
		}
		scope := "repository:" + repo
		if repo == "" {
			scope = "registry:"
		}
		switch access {
		case AuthRead:
			scope += ":pull"
		case AuthWrite:
			scope += ":push"
		case AuthDelete:
			scope += ":delete"
		}
		authHeader := r.Header.Get("Authorization")
		token, ok := strings.CutPrefix(authHeader, "Bearer ")
		if !ok {
			err := unauthorized(w, "Bearer", realmURL.String(), ac.service, scope, "unauthorized")
			if err != nil {
				ac.slog.Error("failed to generate unauthorized header", "err", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		user := ""
		ac.mu.RLock()
		if ato, ok := ac.opaque[token]; ok && time.Since(ato.expires) <= 0 {
			user = ato.user
		}
		ac.mu.RUnlock()
		if !conf.checkAccess(repo, access, user) {
			err := unauthorized(w, "Bearer", realmURL.String(), ac.service, scope, "unauthorized")
			if err != nil {
				ac.slog.Error("failed to generate unauthorized header", "err", err.Error())
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}
		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
}

func (ac *authController) tokenOpaqueGenerate() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		if ac == nil {
			w.WriteHeader(http.StatusInternalServerError)
			ac.slog.Error("auth file is nil", "filename", ac.filename)
			return
		}
		conf, err := ac.getConf()
		if err != nil {
			ac.slog.Warn("failed to refresh auth file", "filename", ac.filename, "err", err.Error())
		}
		if conf == nil {
			ac.slog.Error("auth conf is nil", "filename", ac.filename)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// verify request
		query := r.URL.Query()
		service := query.Get("service")
		if service != ac.service {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// note that scope is not used for opaque tokens
		// check user/pass
		user, passwd, ok := r.BasicAuth()
		if !ok {
			user = ""
		}
		if ok && (user != "" || passwd != "") && !conf.checkCreds(user, passwd) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// generate/save/return opaque token
		sb := make([]byte, 128)
		_, err = rand.Read(sb)
		if err != nil {
			ac.slog.Warn("failed to generate random token", "err", err.Error())
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		token := base64.RawURLEncoding.EncodeToString(sb)
		issued := time.Now().UTC()
		expires := issued.Add(tokenExpire)
		ac.mu.Lock()
		ac.opaque[token] = authTokenOpaque{
			user:    user,
			expires: expires,
		}
		ac.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		response := authTokenResponse{
			Token:     token,
			ExpiresIn: int64(tokenExpire),
			IssuedAt:  issued,
		}
		err = json.NewEncoder(w).Encode(response)
		if err != nil {
			ac.slog.Debug("failed to send token", "err", err.Error())
		}
		// launch a background cleanup job for the token list
		ac.tokenOpaqueCleanup()
	})
}

func (ac *authController) tokenOpaqueCleanup() {
	// attempt to lock the cleanup job
	select {
	case ac.cleanupJob <- struct{}{}:
	default:
		return
	}
	// run the cleanup job in a background goroutine
	go func() {
		for {
			delList := []string{}
			ac.mu.RLock()
			for token, ato := range ac.opaque {
				if time.Since(ato.expires) > 0 {
					delList = append(delList, token)
				}
			}
			ac.mu.RUnlock()
			if len(delList) > 0 {
				ac.mu.Lock()
				for _, token := range delList {
					delete(ac.opaque, token)
				}
				if len(ac.opaque) == 0 {
					// no more tokens to cleanup, unlock the job and stop the goroutine
					<-ac.cleanupJob
					ac.mu.Unlock()
					return
				}
				ac.mu.Unlock()
			}
			time.Sleep(tokenExpire)
		}
	}()
}

// Checks if user/pass match the config.
func (ac *authConf) checkCreds(user, pass string) bool {
	if creds, ok := ac.Users[user]; ok {
		return PassValidate(pass, creds.Cred)
	} else {
		_ = PassValidate(pass, passHashEmpty)
	}
	return false
}

// Checks if a user has the appropriate access. Use an empty user string for anonymous access.
func (ac *authConf) checkAccess(repo string, access AuthAccess, user string) bool {
	bestRepo := ""
	result := false
	for _, acl := range ac.ACLs {
		// skip entries for a worse repo or a non-matching repo
		if len(acl.Repo) < len(bestRepo) || (acl.Repo != repo && (!strings.HasSuffix(acl.Repo, "*") || !strings.HasPrefix(repo, acl.Repo[:len(acl.Repo)-1]))) {
			continue
		}
		// skip entries where user and that user's groups are not in the member list
		userMatch := user == "" && acl.Anonymous
		if user != "" {
			// note, it is possible for authenticated users to have less access than anonymous users
			userMatch = slices.Contains(acl.Members, user)
			if !userMatch && ac.Users[user] != nil && ac.Users[user].groups != nil {
				for group := range ac.Users[user].groups {
					if slices.Contains(acl.Members, group) {
						userMatch = true
						break
					}
				}
			}
		}
		if !userMatch {
			continue
		}
		// found an acl (or better matching acl) for the user and repo, save resulting access
		bestRepo = acl.Repo
		result = slices.Contains(acl.Access, access) || slices.Contains(acl.Access, AuthAll)
	}
	return result
}

// groupMap adds groups to each user entry in the authConf.
func (ac *authConf) groupMap(name string, parents ...string) {
	if slices.Contains(parents, name) {
		return // skip cycles
	}
	if _, ok := ac.Groups[name]; !ok || ac.Groups[name] == nil {
		return // skip missing entries
	}
	for _, member := range ac.Groups[name].Members {
		if _, ok := ac.Users[member]; ok {
			if ac.Users[member] == nil {
				ac.Users[member] = &authUser{}
			}
			if ac.Users[member].groups == nil {
				ac.Users[member].groups = map[string]bool{}
			}
			for _, cur := range append(parents, name) {
				ac.Users[member].groups[cur] = true
			}
		} else {
			// recurse nested groups
			ac.groupMap(member, append(parents, name)...)
		}
	}
}

// NewAuthBasicFile controls access using a config file.
func NewAuthBasicFile(filename string, opts ...AuthOpt) ConfigAuth {
	ac, err := newAuthController(filename)
	ac.realm = authDescription
	for _, opt := range opts {
		opt(ac)
	}
	if err != nil {
		ac.slog.Error("failed to load auth file", "filename", filename, "err", err.Error())
	}
	return ConfigAuth{Handler: ac.basicHandler}
}

// NewAuthBasicStatic uses a list of static plain text logins to control access to the registry.
func NewAuthBasicStatic(logins map[string]string, allowAnonymousRead bool, opts ...AuthOpt) (ConfigAuth, error) {
	ac, err := newAuthController("")
	ac.realm = authDescription
	for _, opt := range opts {
		opt(ac)
	}
	if err != nil {
		return ConfigAuth{}, fmt.Errorf("failed to setup new auth controller: %w", err)
	}
	conf, err := ac.getConf()
	if err != nil {
		return ConfigAuth{}, fmt.Errorf("failed to get conf: %w", err)
	}
	conf.Users = map[string]*authUser{}
	userList := make([]string, 0, len(logins))
	for user, pass := range logins {
		hash, err := PassHash(pass, PassAlgoDefault)
		if err != nil {
			return ConfigAuth{}, fmt.Errorf("failed to hash password for %s: %w", user, err)
		}
		conf.Users[user] = &authUser{
			Cred: hash,
		}
		userList = append(userList, user)
	}
	conf.ACLs = append(conf.ACLs, authACL{
		Repo:    "*",
		Access:  []AuthAccess{AuthRead, AuthWrite, AuthDelete},
		Members: userList,
	})
	if allowAnonymousRead {
		conf.ACLs = append(conf.ACLs, authACL{
			Repo:      "*",
			Access:    []AuthAccess{AuthRead},
			Anonymous: true,
		})
	}
	return ConfigAuth{Handler: ac.basicHandler}, nil
}

// NewAuthTokenOpaque controls access using a config file and a local opaque token server.
// Many clients will require [WithAuthRealm] set to an externally resolvable URL to the /token API (e.g. https://registry.example.org/token).
func NewAuthTokenOpaque(filename string, opts ...AuthOpt) ConfigAuth {
	ac, err := newAuthController(filename)
	ac.realm = "/token"
	ac.service = authDescription
	for _, opt := range opts {
		opt(ac)
	}
	if err != nil {
		ac.slog.Error("failed to load auth file", "filename", filename, "err", err.Error())
	}
	return ConfigAuth{Handler: ac.tokenOpaqueHandler, Token: ac.tokenOpaqueGenerate()}
}

func unauthorized(w http.ResponseWriter, authType, realm, service, scope, errMsg string) error {
	if authType == "" || realm == "" {
		return fmt.Errorf("authType and realm are required")
	}
	h := fmt.Sprintf("%s realm=%q", authType, realm)
	if service != "" {
		h = fmt.Sprintf("%s,service=%q", h, service)
	}
	if scope != "" {
		h = fmt.Sprintf("%s,scope=%q", h, scope)
	}
	if errMsg != "" {
		h = fmt.Sprintf("%s,error=%q", h, errMsg)
	}
	w.Header().Add("WWW-Authenticate", h)
	w.WriteHeader(http.StatusUnauthorized)
	_ = types.ErrRespJSON(w, types.ErrInfoUnauthorized("scope = "+scope))
	return nil
}

type PassAlgo int

const (
	PassAlgoDefault PassAlgo = iota
	PassAlgoBcrypt
)

// TODO: consider supporting other algorithms based on the prefix
// well known hash prefixes (see <https://en.wikipedia.org/wiki/Bcrypt>)
// $1$: MD5 based crypt (insecure)
// $2$: bcrypt (including $2a$, $2b$, $2x$, and $2y$)
// $sha1$: SHA-1 based crypt
// {SHA}: SHA-1
// $5$: SHA-256-based crypt
// $6$: SHA-512-based crypt

var passHashEmpty, _ = PassHashBcrypt("")

func PassHash(input string, algo PassAlgo) (string, error) {
	switch algo {
	// TODO: add support for more algorithms
	default: // Bcrypt is the default
		return PassHashBcrypt(input)
	}
}

func PassHashBcrypt(input string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(input), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func PassValidate(input, hash string) bool {
	// determine algorithm from hash
	switch {
	case strings.HasPrefix(hash, "$2y$") || strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2x$"):
		return PassValidateBcrypt(input, hash)
		// TODO: add support for more algorithms
	}
	// always run a validation for timing attacks
	_ = PassValidateBcrypt(input, passHashEmpty)
	return false
}

func PassValidateBcrypt(input, hash string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(input))
	return err == nil
}
