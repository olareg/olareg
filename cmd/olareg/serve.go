package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/olareg/olareg"
	"github.com/olareg/olareg/config"
)

type serveOpts struct {
	root        *rootOpts
	addr        string
	port        int
	tlsCert     string
	tlsKey      string
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
	newCmd.Flags().IntVar(&opts.port, "port", 5000, "listener port")
	newCmd.Flags().StringVar(&opts.tlsCert, "tls-cert", "", "TLS certificate for HTTPS")
	newCmd.Flags().StringVar(&opts.tlsKey, "tls-key", "", "TLS key for HTTPS")
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
		HTTP: config.ConfigHTTP{
			Addr:     fmt.Sprintf("%s:%d", opts.addr, opts.port),
			CertFile: opts.tlsCert,
			KeyFile:  opts.tlsKey,
		},
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
	s := olareg.New(conf)
	// include signal handler to gracefully shutdown
	ctx, cancel := context.WithCancel(context.Background())
	cleanShutdown := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		<-sig
		opts.root.log.Debug("Interrupt received, shutting down")
		err := s.Shutdown(ctx)
		if err != nil {
			opts.root.log.Warn("graceful shutdown failed", "err", err)
		}
		// clean shutdown
		cancel()
		close(cleanShutdown)
	}()
	// run the server
	err = s.Run(ctx)
	if err != nil {
		return err
	}
	<-cleanShutdown
	return nil
}
