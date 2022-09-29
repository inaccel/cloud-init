package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	secrets "k8s.io/kubernetes/pkg/volume/secret"
	"k8s.io/kubernetes/pkg/volume/util"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cloudinit "kubevirt.io/kubevirt/pkg/cloud-init"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/yaml"
)

func IsHostDevice(hostDevice kubevirtv1.HostDevice, deviceName string, index int) bool {
	return hostDevice.Name == fmt.Sprintf("inaccel%d", index)
}

func HostDevice(deviceName string, index int) kubevirtv1.HostDevice {
	return kubevirtv1.HostDevice{
		Name:       fmt.Sprintf("inaccel%d", index),
		DeviceName: deviceName,
	}
}

type CloudConfig struct {
	InAccel corev1.ResourceList `yaml:"inaccel"`
}

type VirtualMachineInstanceDefaulter struct{}

func (VirtualMachineInstanceDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	virtualMachineInstance, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return fmt.Errorf("virtual machine instance defaulter did not understand object: %T", obj)
	}

	api, ok := ctx.Value(apiKey{}).(client.Client)
	if !ok {
		kube, err := config.GetConfig()
		if err != nil {
			return err
		}
		api, err = client.New(kube, client.Options{})
		if err != nil {
			return err
		}
	}

	tempDir, err := os.MkdirTemp(".", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	for i := range virtualMachineInstance.Spec.Volumes {
		var userDataSecretRef *corev1.LocalObjectReference
		if virtualMachineInstance.Spec.Volumes[i].CloudInitConfigDrive != nil {
			if virtualMachineInstance.Spec.Volumes[i].CloudInitConfigDrive.UserDataSecretRef != nil {
				userDataSecretRef = virtualMachineInstance.Spec.Volumes[i].CloudInitConfigDrive.UserDataSecretRef
			}
		}
		if virtualMachineInstance.Spec.Volumes[i].CloudInitNoCloud != nil {
			if virtualMachineInstance.Spec.Volumes[i].CloudInitNoCloud.UserDataSecretRef != nil {
				userDataSecretRef = virtualMachineInstance.Spec.Volumes[i].CloudInitNoCloud.UserDataSecretRef
			}
		}

		if userDataSecretRef != nil {
			secret := &corev1.Secret{}
			if err := api.Get(ctx, client.ObjectKey{
				Namespace: virtualMachineInstance.Namespace,
				Name:      userDataSecretRef.Name,
			}, secret); err != nil {
				return err
			}

			defaultMode := corev1.SecretVolumeSourceDefaultMode
			payload, err := secrets.MakePayload([]corev1.KeyToPath{
				{
					Key:  "userdata",
					Path: filepath.Join(virtualMachineInstance.Spec.Volumes[i].Name, "userdata"),
				},
				{
					Key:  "userData",
					Path: filepath.Join(virtualMachineInstance.Spec.Volumes[i].Name, "userData"),
				},
			}, secret, &defaultMode, true)
			if err != nil {
				return err
			}

			w, err := util.NewAtomicWriter(tempDir, virtualMachineInstance.Spec.Volumes[i].Name)
			if err != nil {
				return err
			}

			if err := w.Write(payload); err != nil {
				return err
			}
		}
	}

	virtualMachineInstanceCopy := virtualMachineInstance.DeepCopyObject().(*kubevirtv1.VirtualMachineInstance)
	cloudInitData, err := cloudinit.ReadCloudInitVolumeDataSource(virtualMachineInstanceCopy, tempDir)
	if err != nil {
		return err
	}

	if cloudInitData != nil {
		var cloudConfig CloudConfig
		if err := yaml.Unmarshal([]byte(cloudInitData.UserData), &cloudConfig); err != nil {
			return err
		}

		var index int
		for resourceName, resourceQuantity := range cloudConfig.InAccel {
			for range make([]interface{}, resourceQuantity.Value()) {
				hostDeviceExists := false
				for i := range virtualMachineInstance.Spec.Domain.Devices.HostDevices {
					if IsHostDevice(virtualMachineInstance.Spec.Domain.Devices.HostDevices[i], string(resourceName), index) {
						hostDeviceExists = true
						virtualMachineInstance.Spec.Domain.Devices.HostDevices[i] = HostDevice(string(resourceName), index)
					}
				}
				if !hostDeviceExists {
					virtualMachineInstance.Spec.Domain.Devices.HostDevices = append(virtualMachineInstance.Spec.Domain.Devices.HostDevices, HostDevice(string(resourceName), index))
				}

				index++
			}
		}
	}

	return nil
}
