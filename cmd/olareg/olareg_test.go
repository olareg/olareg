package main

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

type cobraTestOpts struct {
	stdin io.Reader
}

func cobraTest(t *testing.T, opts *cobraTestOpts, args ...string) (string, error) {
	t.Helper()

	buf := new(bytes.Buffer)
	cmd := newRootCmd()
	if opts != nil && opts.stdin != nil {
		cmd.SetIn(opts.stdin)
	}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(args)

	err := cmd.Execute()
	return strings.TrimSpace(buf.String()), err
}

func TestVersion(t *testing.T) {
	tt := []struct {
		name        string
		args        []string
		expectErr   error
		expectOut   string
		outContains bool
	}{
		{
			name:        "no-format",
			args:        []string{"version"},
			expectOut:   "VCSTag",
			outContains: true,
		},
		{
			name:        "format-tag",
			args:        []string{"version", "--format", "Tag: {{.VCSTag}}"},
			expectOut:   "Tag: ",
			outContains: true,
		},
	}
	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			out, err := cobraTest(t, nil, tc.args...)
			if tc.expectErr != nil {
				if err == nil {
					t.Errorf("did not receive expected error: %v", tc.expectErr)
				} else if !errors.Is(err, tc.expectErr) && err.Error() != tc.expectErr.Error() {
					t.Errorf("unexpected error, received %v, expected %v", err, tc.expectErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("returned unexpected error: %v", err)
			}
			if (!tc.outContains && out != tc.expectOut) || (tc.outContains && !strings.Contains(out, tc.expectOut)) {
				t.Errorf("unexpected output, expected %s, received %s", tc.expectOut, out)
			}
		})
	}
}
