package config

import (
	"fmt"
	"net/http"
)

type AuthAccess int

const (
	AuthRead AuthAccess = iota
	AuthWrite
	AuthDelete
	AuthUnknown
)

// AuthHandler creates a new [http.Handler] that checks a request for the needed access.
// It may refuse the request with a 401 status, www-authenticate header, and then return.
// It may allow the request by calling the next handler without modifying the request or response writer.
type AuthHandler func(repo string, access AuthAccess, next http.Handler) http.Handler

// NewAuthBasicStatic uses a list of static plain text logins to control access to the registry.
func NewAuthBasicStatic(logins map[string]string, allowAnonymousRead bool) AuthHandler {
	ab := authBasic{
		allowAnonymousRead: allowAnonymousRead,
		checkPasswd: func(user, passwd string) bool {
			if p, ok := logins[user]; ok && p == passwd {
				return true
			}
			return false
		},
		checkAccess: func(user, repo string, access AuthAccess) bool { return true },
		realm:       "olareg registry",
	}
	return ab.Handle
}

type authBasic struct {
	allowAnonymousRead bool
	checkPasswd        func(user, passwd string) bool
	checkAccess        func(user, repo string, access AuthAccess) bool
	realm              string
}

func (ab *authBasic) Handle(repo string, access AuthAccess, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// extract the user and verify access
		user, passwd, ok := r.BasicAuth()
		if (!ab.allowAnonymousRead || access != AuthRead) &&
			(!ok ||
				ab.checkPasswd == nil || !ab.checkPasswd(user, passwd) ||
				ab.checkAccess == nil || !ab.checkAccess(user, repo, access)) {
			err := unauthorized(w, "Basic", ab.realm, "", "", "")
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
			}
			return
		}

		if next != nil {
			next.ServeHTTP(w, r)
		}
	})
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
	return nil
}
