package main

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg"
	"github.com/olareg/olareg/config"
)

type serveOpts struct {
	root      *rootOpts
	addr      string
	port      int
	storeType string
	storeDir  string
}

func newServeCmd(root *rootOpts) *cobra.Command {
	opts := serveOpts{
		root: root,
	}
	newCmd := &cobra.Command{
		Use:   "serve",
		Short: "Run a registry server",
		Long:  "Run a registry server",
		RunE:  opts.run,
	}
	newCmd.Flags().StringVar(&opts.addr, "addr", "", "listener interface or address")
	newCmd.Flags().IntVar(&opts.port, "port", 80, "listener port")
	newCmd.Flags().StringVar(&opts.storeDir, "dir", ".", "root directory for storage")
	newCmd.Flags().StringVar(&opts.storeType, "store-type", "dir", "storage type (dir, mem)")
	return newCmd
}

func (opts *serveOpts) run(cmd *cobra.Command, args []string) error {
	var storeType config.Store
	err := storeType.UnmarshalText([]byte(opts.storeType))
	if err != nil {
		return fmt.Errorf("unable to parse store type %s: %w", opts.storeType, err)
	}
	conf := config.Config{
		StoreType: storeType,
		RootDir:   opts.storeDir,
		Log:       opts.root.log,
	}
	handler := olareg.New(conf)
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", opts.addr, opts.port))
	if err != nil {
		return fmt.Errorf("unable to start listener: %w", err)
	}
	opts.root.log.Info("listening for connections", "addr", opts.addr, "port", opts.port)
	s := &http.Server{
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           handler,
	}
	// TODO: add signal handler to shutdown
	err = s.Serve(listener)
	// TODO: handle different error responses, graceful exit should not error
	if err != nil {
		return err
	}
	return nil
}
