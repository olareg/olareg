package olareg_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"

	"github.com/olareg/olareg"
	"github.com/olareg/olareg/config"
)

func ExampleNew_test() {
	ctx := context.Background()
	// create a new olareg handler backed by memory
	regHandler := olareg.New(config.Config{
		Storage: config.ConfigStorage{
			StoreType: config.StoreMem,
		},
	})
	// start the handler using httptest for unit tests
	ts := httptest.NewServer(regHandler)
	defer ts.Close()
	defer regHandler.Close()
	// ping the /v2/ endpoint
	u, err := url.Parse(ts.URL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to parse url: %v", err)
		return
	}
	u.Path = "/v2/"
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to generate request: %v", err)
		return
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to send request: %v", err)
		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to read body: %v", err)
		return
	}
	fmt.Printf("body: %s\n", string(body))
	// Output: body: {}
}

func ExampleNew_mem() {
	ctx := context.Background()
	// create a server backed by memory, listening on port 5000
	regHandler := olareg.New(config.Config{
		HTTP: config.ConfigHTTP{
			Addr: ":5000",
		},
		Storage: config.ConfigStorage{
			StoreType: config.StoreMem,
		},
	})
	// run the server
	err := regHandler.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v", err)
		return
	}
	// use the server
	// ...
	// shutdown the server
	err = regHandler.Shutdown(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shutdown failed: %v", err)
		return
	}
}

func ExampleNew_directory() {
	ctx := context.Background()
	// create a server backed by a local directory, listening on port 5000
	regHandler := olareg.New(config.Config{
		HTTP: config.ConfigHTTP{
			Addr: ":5000",
		},
		Storage: config.ConfigStorage{
			StoreType: config.StoreDir,
			RootDir:   "/path/to/storage",
		},
	})
	// run the server
	err := regHandler.Run(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v", err)
		return
	}
	// use the server
	// ...
	// shutdown the server
	err = regHandler.Shutdown(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "shutdown failed: %v", err)
		return
	}
}
