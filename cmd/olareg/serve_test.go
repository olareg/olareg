package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"
)

func TestServe(t *testing.T) {
	timeout := time.Millisecond * 250
	tt := []struct {
		name        string
		args        []string
		expectErr   error
		expectOut   string
		outContains bool
	}{
		{
			name: "no-args",
			args: []string{"serve"},
		},
		{
			name:      "unknown-store",
			args:      []string{"serve", "--store-type", "unknown"},
			expectErr: fmt.Errorf(`unable to parse store type unknown: unknown store value "unknown"`),
		},
		{
			name: "port",
			args: []string{"serve", "--port", "12345"},
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			out, err := cobraTest(t, &cobraTestOpts{timeout: timeout}, tc.args...)
			if tc.expectErr != nil {
				if err == nil {
					t.Errorf("did not receive expected error: %v", tc.expectErr)
				} else if !errors.Is(err, tc.expectErr) && err.Error() != tc.expectErr.Error() {
					t.Errorf("unexpected error, received %v, expected %v", err, tc.expectErr)
				}
				return
			}
			if err != nil {
				var netErr *net.OpError
				if errors.As(err, &netErr) && netErr.Op == "listen" {
					t.Skipf("skipping, unable to listen: %v", err)
				}
				t.Fatalf("returned unexpected error: %v", err)
			}
			if (!tc.outContains && out != tc.expectOut) || (tc.outContains && !strings.Contains(out, tc.expectOut)) {
				t.Errorf("unexpected output, expected %s, received %s", tc.expectOut, out)
			}
		})
	}
}
