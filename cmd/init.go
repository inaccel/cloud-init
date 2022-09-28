package main

import (
	"os"
	"path/filepath"
	"strings"

	certmanagerv1 "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/cert-manager/cert-manager/pkg/util/pki"
	"github.com/urfave/cli/v2"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

var initCommand = &cli.Command{
	Name:      "init",
	Usage:     "Update SSL certification/key files",
	ArgsUsage: " ",
	Action: func(context *cli.Context) error {
		kube, err := config.GetConfig()
		if err != nil {
			return err
		}
		api, err := client.New(kube, client.Options{})
		if err != nil {
			return err
		}

		mutatingWebhookConfiguration := &admissionregistrationv1.MutatingWebhookConfiguration{}
		if err := api.Get(context.Context, client.ObjectKey{
			Name: os.Getenv("MUTATING_WEBHOOK_CONFIGURATION_NAME"),
		}, mutatingWebhookConfiguration); err != nil {
			return err
		}

		var cert string
		if _, err := os.Stat(context.String("cert")); err == nil {
			cert = context.String("cert")
		}
		var key string
		if _, err := os.Stat(context.String("key")); err == nil {
			key = context.String("key")
		}

		var caBundle []byte
		if len(cert) == 0 || len(key) == 0 {
			certificate := &certmanagerv1.Certificate{
				Spec: certmanagerv1.CertificateSpec{
					Subject: &certmanagerv1.X509Subject{
						Organizations: []string{
							"inaccel.com",
						},
					},
					DNSNames: []string{
						mutatingWebhookConfiguration.Webhooks[0].ClientConfig.Service.Name,
						strings.Join([]string{
							mutatingWebhookConfiguration.Webhooks[0].ClientConfig.Service.Name,
							mutatingWebhookConfiguration.Webhooks[0].ClientConfig.Service.Namespace,
						}, "."),
						strings.Join([]string{
							mutatingWebhookConfiguration.Webhooks[0].ClientConfig.Service.Name,
							mutatingWebhookConfiguration.Webhooks[0].ClientConfig.Service.Namespace,
							"svc",
						}, "."),
					},
				},
			}

			ca, err := pki.GenerateTemplate(certificate)
			if err != nil {
				return err
			}

			caPrivateKey, err := pki.GeneratePrivateKeyForCertificate(certificate)
			if err != nil {
				return err
			}

			caPrivateKeyBytes, err := pki.EncodePrivateKey(caPrivateKey, certmanagerv1.PKCS8)
			if err != nil {
				return err
			}

			caPublicKey, err := pki.PublicKeyForPrivateKey(caPrivateKey)
			if err != nil {
				return err
			}

			caBytes, _, err := pki.SignCertificate(ca, ca, caPublicKey, caPrivateKey)
			if err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(context.String("cert")), os.ModePerm); err != nil {
				return err
			}
			if err := os.WriteFile(context.String("cert"), caBytes, os.ModePerm); err != nil {
				return err
			}

			if err := os.MkdirAll(filepath.Dir(context.String("key")), os.ModePerm); err != nil {
				return err
			}
			if err := os.WriteFile(context.String("key"), caPrivateKeyBytes, os.ModePerm); err != nil {
				return err
			}

			caBundle = caBytes
		}

		for i := range mutatingWebhookConfiguration.Webhooks {
			if mutatingWebhookConfiguration.Webhooks[i].Name == "cloud-init.inaccel.com" {
				if caBundle != nil {
					mutatingWebhookConfiguration.Webhooks[i].ClientConfig.CABundle = caBundle
				}

				mutatingWebhookConfiguration.Webhooks[i].Rules = []admissionregistrationv1.RuleWithOperations{
					{
						Operations: []admissionregistrationv1.OperationType{admissionregistrationv1.Create},
						Rule: admissionregistrationv1.Rule{
							APIGroups:   []string{"kubevirt.io"},
							APIVersions: []string{"v1"},
							Resources:   []string{"virtualmachineinstances"},
						},
					},
				}
			}
		}

		if err := api.Update(context.Context, mutatingWebhookConfiguration); err != nil {
			return err
		}

		return nil
	},
}
