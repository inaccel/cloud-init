package main

import (
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/bombsimon/logrusr/v3"
	"github.com/inaccel/cloud-init/internal"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"
	"sigs.k8s.io/controller-runtime/pkg/certwatcher"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var version string

func main() {
	app := &cli.App{
		Name:    "cloud-init",
		Version: version,
		Usage:   "A self-sufficient runtime for accelerators.",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:  "cert",
				Usage: "SSL certification file",
				Value: "/etc/inaccel/certs/ssl.pem",
			},
			&cli.StringFlag{
				Name:  "key",
				Usage: "SSL key file",
				Value: "/etc/inaccel/private/ssl.key",
			},
			&cli.BoolFlag{
				Name:    "debug",
				Aliases: []string{"d"},
				Usage:   "enable debug output",
			},
		},
		Before: func(context *cli.Context) error {
			log.SetOutput(io.Discard)

			logrus.SetFormatter(new(logrus.JSONFormatter))

			if context.Bool("debug") {
				logrus.SetLevel(logrus.DebugLevel)
			}

			return nil
		},
		Action: func(context *cli.Context) error {
			handler, err := admission.StandaloneWebhook(internal.Webhook, admission.StandaloneOptions{
				Logger: logrusr.New(logrus.StandardLogger()),
			})
			if err != nil {
				return err
			}

			http.Handle("/", handler)

			watcher, err := certwatcher.New(context.String("cert"), context.String("key"))
			if err != nil {
				return err
			}

			go func() {
				if err := watcher.Start(context.Context); err != nil {
					logrus.Error(err)
				}
			}()

			server := &http.Server{
				TLSConfig: &tls.Config{
					GetCertificate: watcher.GetCertificate,
				},
			}
			return server.ListenAndServeTLS("", "")
		},
		Commands: []*cli.Command{
			initCommand,
		},
	}

	if err := app.Run(os.Args); err != nil {
		logrus.Fatal(err)
	}
}
