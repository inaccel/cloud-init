package internal

import (
	"context"
	"net/http"

	kubevirtv1 "kubevirt.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
)

var Webhook = admission.WithCustomDefaulter(new(kubevirtv1.VirtualMachineInstance), VirtualMachineInstanceDefaulter{})

func init() {
	Webhook.WithContextFunc = func(ctx context.Context, _ *http.Request) context.Context {
		kube, err := config.GetConfig()
		if err != nil {
			return ctx
		}
		api, err := client.New(kube, client.Options{})
		if err != nil {
			return ctx
		}

		return context.WithValue(ctx, "api", api)
	}
}
