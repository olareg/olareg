package config

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/olareg/olareg/types"
)

func TestAuthAccess(t *testing.T) {
	tt := []struct {
		a AuthAccess
		s string
	}{
		{
			a: AuthUnknown,
			s: "",
		},
		{
			a: AuthRead,
			s: "read",
		},
		{
			a: AuthWrite,
			s: "write",
		},
		{
			a: AuthDelete,
			s: "delete",
		},
	}
	for _, tc := range tt {
		t.Run(tc.s, func(t *testing.T) {
			marshal, err := tc.a.MarshalText()
			if err != nil {
				t.Fatalf("failed to marshal %d", int(tc.a))
			}
			if string(marshal) != tc.s {
				t.Errorf("marshal mismatch, expected %s, received %s", tc.s, string(marshal))
			}
			var unmarshal AuthAccess
			err = unmarshal.UnmarshalText([]byte(tc.s))
			if err != nil {
				t.Fatalf("failed to unmarshal %s", tc.s)
			}
			if unmarshal != tc.a {
				t.Errorf("unmarshal mismatch, expected %d, received %d", int(tc.a), int(unmarshal))
			}
		})
	}
}

func TestAuthBasicFile(t *testing.T) {
	handleOK := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tt := []struct {
		name       string
		file       string
		repo       string
		access     AuthAccess
		user, pass string
		status     int
	}{
		{
			name:   "invalid file",
			file:   "./testdata/auth_invalid.yaml",
			repo:   "test",
			access: AuthRead,
			status: http.StatusUnauthorized,
		},
		{
			name:   "ping",
			file:   "./testdata/auth_basic.yaml",
			access: AuthRead,
			status: http.StatusUnauthorized,
		},
		{
			name:   "ping alice",
			file:   "./testdata/auth_basic.yaml",
			user:   "alice",
			pass:   "password1",
			access: AuthRead,
			status: http.StatusOK,
		},
		{
			name:   "ping alice wrong pass",
			file:   "./testdata/auth_basic.yaml",
			user:   "alice",
			pass:   "password2",
			access: AuthRead,
			status: http.StatusUnauthorized,
		},
		{
			name:   "guest bob",
			file:   "./testdata/auth_basic.yaml",
			repo:   "guest/project-a",
			user:   "bob",
			pass:   "password2",
			access: AuthRead,
			status: http.StatusOK,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			confAuth := NewAuthBasicFile(tc.file)
			h := confAuth.Handler(tc.repo, tc.access, handleOK)
			req := httptest.NewRequest("GET", "/test", bytes.NewBuffer([]byte{}))
			if tc.user != "" {
				req.SetBasicAuth(tc.user, tc.pass)
			}
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)
			if resp.Code != tc.status {
				t.Errorf("expected status %d, received %d", tc.status, resp.Code)
			}
		})
	}
}

func TestAuthBasicStatic(t *testing.T) {
	handleOK := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	tt := []struct {
		name       string
		logins     map[string]string
		anonymous  bool
		repo       string
		access     AuthAccess
		user, pass string
		status     int
	}{
		{
			name:      "anonymous allowed",
			logins:    map[string]string{},
			anonymous: true,
			repo:      "test",
			access:    AuthRead,
			status:    http.StatusOK,
		},
		{
			name:      "anonymous denied",
			logins:    map[string]string{},
			anonymous: false,
			repo:      "test",
			access:    AuthRead,
			status:    http.StatusUnauthorized,
		},
		{
			name:   "valid login",
			logins: map[string]string{"alice": "password1"},
			repo:   "test",
			access: AuthWrite,
			user:   "alice",
			pass:   "password1",
			status: http.StatusOK,
		},
		{
			name:   "bad password",
			logins: map[string]string{"alice": "password1"},
			repo:   "test",
			access: AuthWrite,
			user:   "alice",
			pass:   "password2",
			status: http.StatusUnauthorized,
		},
		{
			name:   "invalid login",
			logins: map[string]string{"alice": "password1"},
			repo:   "test",
			access: AuthWrite,
			user:   "bob",
			pass:   "password2",
			status: http.StatusUnauthorized,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			confAuth, err := NewAuthBasicStatic(tc.logins, tc.anonymous)
			if err != nil {
				t.Fatalf("failed to setup static auth: %v", err)
			}
			h := confAuth.Handler(tc.repo, tc.access, handleOK)
			req := httptest.NewRequest("GET", "/test", bytes.NewBuffer([]byte{}))
			if tc.user != "" {
				req.SetBasicAuth(tc.user, tc.pass)
			}
			resp := httptest.NewRecorder()
			h.ServeHTTP(resp, req)
			if resp.Code != tc.status {
				t.Errorf("expected status %d, received %d", tc.status, resp.Code)
			}
		})
	}
}

func TestAuthFile(t *testing.T) {
	tt := []struct {
		name   string
		file   string
		newErr error
		getErr error
		conf   authConf
	}{
		{
			name: "valid",
			file: "./testdata/auth_valid.yaml",
			conf: authConf{
				Users: map[string]*authUser{
					"alice": {
						Cred:   "$2a$10$AeIxYk02nNYLrmkEIQRSse4DsFH0M9exGec0FbSDSY0fPSZ9chPoa",
						groups: map[string]bool{"direct": true, "loop": true, "root": true},
					},
					"bob": {
						Cred:   "$2a$10$4iTFUSDqPMFRdG0ukcoNzePmjmblKtVCQF2Q50aoRymIat5TM/mXy",
						groups: map[string]bool{"direct": true, "loop": true, "root": true},
					},
				},
				Groups: map[string]*authGroup{
					"direct": {
						Members: []string{"alice", "bob"},
					},
					"empty": nil,
					"loop": {
						Members: []string{"root"},
					},
					"root": {
						Members: []string{"direct", "empty", "loop"},
					},
					"unknown": {
						Members: []string{"bar"},
					},
				},
				ACLs: nil,
			},
		},
		{
			name:   "invalid",
			file:   "./testdata/auth_invalid.yaml",
			getErr: types.ErrParsingFailed,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			af, err := newAuthController(tc.file)
			if tc.newErr != nil {
				if !errors.Is(err, tc.newErr) {
					t.Errorf("error expected %#v, received %#v", tc.newErr, err)
				}
			}
			if err != nil {
				t.Fatalf("unexpected new error: %v", err)
			}
			conf, err := af.getConf()
			if tc.getErr != nil {
				if !errors.Is(err, tc.getErr) {
					t.Errorf("error expected %#v, received %#v", tc.getErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected get error: %v", err)
			}
			if !reflect.DeepEqual(*conf, tc.conf) {
				t.Errorf("Conf expected %v, received %v", tc.conf, *af.conf)
			}
		})
	}
}

func TestPassHash(t *testing.T) {
	tt := []struct {
		name string
		algo PassAlgo
		pass string
	}{
		{
			name: "bcrypt hello world",
			algo: PassAlgoBcrypt,
			pass: "hello world",
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			hash, err := PassHash(tc.pass, tc.algo)
			if err != nil {
				t.Fatalf("failed to hash password: %v", err)
			}
			dup, err := PassHash(tc.pass, tc.algo)
			if err != nil {
				t.Fatalf("failed to hash password: %v", err)
			}
			if hash == dup {
				t.Errorf("passwords are not correctly salted, hash and dup match")
			}
			if !PassValidate(tc.pass, hash) {
				t.Errorf("password does not validate to its hash")
			}
		})
	}
}
