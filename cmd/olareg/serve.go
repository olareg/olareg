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
	root        *rootOpts
	addr        string
	port        int
	storeType   string
	storeDir    string
	apiPush     bool
	apiDelete   bool
	apiBlobDel  bool
	apiReferrer bool
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
	newCmd.Flags().BoolVar(&opts.apiPush, "api-push", true, "enable push APIs")
	newCmd.Flags().BoolVar(&opts.apiDelete, "api-delete", true, "enable delete APIs")
	newCmd.Flags().BoolVar(&opts.apiBlobDel, "api-blob-delete", false, "enable blob delete API")
	newCmd.Flags().BoolVar(&opts.apiReferrer, "api-referrer", true, "enable referrer API")
	return newCmd
}

func (opts *serveOpts) run(cmd *cobra.Command, args []string) error {
	var storeType config.Store
	err := storeType.UnmarshalText([]byte(opts.storeType))
	if err != nil {
		return fmt.Errorf("unable to parse store type %s: %w", opts.storeType, err)
	}
	conf := config.Config{
		Storage: config.ConfigStorage{
			StoreType: storeType,
			RootDir:   opts.storeDir,
		},
		Log: opts.root.log,
		API: config.ConfigAPI{
			PushEnabled:   &opts.apiPush,
			DeleteEnabled: &opts.apiDelete,
			Blob:          config.ConfigAPIBlob{DeleteEnabled: &opts.apiBlobDel},
			Referrer:      config.ConfigAPIReferrer{Enabled: &opts.apiReferrer},
		},
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
