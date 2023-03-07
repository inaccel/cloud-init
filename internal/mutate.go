package internal

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	secrets "k8s.io/kubernetes/pkg/volume/secret"
	"k8s.io/kubernetes/pkg/volume/util"
	kubevirtv1 "kubevirt.io/api/core/v1"
	cloudinit "kubevirt.io/kubevirt/pkg/cloud-init"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/yaml"
)

func IsHostDevice(hostDevice kubevirtv1.HostDevice) bool {
	return strings.HasPrefix(hostDevice.Name, "inaccel")
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

type VirtualMachineDefaulter struct {
	client.Client
}

func NewVirtualMachineDefaulter(api client.Client) *VirtualMachineDefaulter {
	return &VirtualMachineDefaulter{api}
}

func (d *VirtualMachineDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	virtualMachine, ok := obj.(*kubevirtv1.VirtualMachine)
	if !ok {
		return fmt.Errorf("virtual machine defaulter did not understand object: %T", obj)
	}

	tempDir, err := os.MkdirTemp(".", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	for i := range virtualMachine.Spec.Template.Spec.Volumes {
		var userDataSecretRef *corev1.LocalObjectReference
		if virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitConfigDrive != nil {
			if virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitConfigDrive.UserDataSecretRef != nil {
				userDataSecretRef = virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitConfigDrive.UserDataSecretRef
			}
		}
		if virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitNoCloud != nil {
			if virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitNoCloud.UserDataSecretRef != nil {
				userDataSecretRef = virtualMachine.Spec.Template.Spec.Volumes[i].CloudInitNoCloud.UserDataSecretRef
			}
		}

		if userDataSecretRef != nil {
			secret := &corev1.Secret{}
			if err := d.Get(ctx, client.ObjectKey{
				Namespace: virtualMachine.Namespace,
				Name:      userDataSecretRef.Name,
			}, secret); err != nil {
				return err
			}

			defaultMode := corev1.SecretVolumeSourceDefaultMode
			payload, err := secrets.MakePayload([]corev1.KeyToPath{
				{
					Key:  "userdata",
					Path: filepath.Join(virtualMachine.Spec.Template.Spec.Volumes[i].Name, "userdata"),
				},
				{
					Key:  "userData",
					Path: filepath.Join(virtualMachine.Spec.Template.Spec.Volumes[i].Name, "userData"),
				},
			}, secret, &defaultMode, true)
			if err != nil {
				return err
			}

			w, err := util.NewAtomicWriter(tempDir, virtualMachine.Spec.Template.Spec.Volumes[i].Name)
			if err != nil {
				return err
			}

			if err := w.Write(payload); err != nil {
				return err
			}
		}
	}

	virtualMachineCopy := virtualMachine.DeepCopyObject().(*kubevirtv1.VirtualMachine)
	cloudInitData, err := cloudinit.ReadCloudInitVolumeDataSource(&kubevirtv1.VirtualMachineInstance{
		Spec: virtualMachineCopy.Spec.Template.Spec,
	}, tempDir)
	if err != nil {
		return err
	}

	if cloudInitData != nil {
		var cloudConfig CloudConfig
		if err := yaml.Unmarshal([]byte(cloudInitData.UserData), &cloudConfig); err != nil {
			return err
		}

		var hostDevices []kubevirtv1.HostDevice
		for i := range virtualMachine.Spec.Template.Spec.Domain.Devices.HostDevices {
			if !IsHostDevice(virtualMachine.Spec.Template.Spec.Domain.Devices.HostDevices[i]) {
				hostDevices = append(hostDevices, virtualMachine.Spec.Template.Spec.Domain.Devices.HostDevices[i])
			}
		}
		var index int
		for resourceName, resourceQuantity := range cloudConfig.InAccel {
			for range make([]interface{}, resourceQuantity.Value()) {
				hostDevices = append(hostDevices, HostDevice(string(resourceName), index))

				index++
			}
		}
		virtualMachine.Spec.Template.Spec.Domain.Devices.HostDevices = hostDevices
	}

	return nil
}

type VirtualMachineReconciler struct {
	*VirtualMachineDefaulter
}

func NewVirtualMachineReconciler(api client.Client) *VirtualMachineReconciler {
	return &VirtualMachineReconciler{&VirtualMachineDefaulter{api}}
}

func (r *VirtualMachineReconciler) Reconcile(ctx context.Context, o reconcile.Request) (reconcile.Result, error) {
	vm := &kubevirtv1.VirtualMachine{}

	if err := r.Get(ctx, o.NamespacedName, vm); err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	if err := r.Default(ctx, vm); err != nil {
		return reconcile.Result{}, err
	}

	if err := r.Update(ctx, vm); err != nil {
		return reconcile.Result{}, err
	}

	return reconcile.Result{}, nil
}

type VirtualMachineInstanceDefaulter struct {
	client.Client
}

func NewVirtualMachineInstanceDefaulter(api client.Client) *VirtualMachineInstanceDefaulter {
	return &VirtualMachineInstanceDefaulter{api}
}

func (d *VirtualMachineInstanceDefaulter) Default(ctx context.Context, obj runtime.Object) error {
	virtualMachineInstance, ok := obj.(*kubevirtv1.VirtualMachineInstance)
	if !ok {
		return fmt.Errorf("virtual machine instance defaulter did not understand object: %T", obj)
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
			if err := d.Get(ctx, client.ObjectKey{
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
	cloudInitData, err := cloudinit.ReadCloudInitVolumeDataSource(&kubevirtv1.VirtualMachineInstance{
		Spec: virtualMachineInstanceCopy.Spec,
	}, tempDir)
	if err != nil {
		return err
	}

	if cloudInitData != nil {
		var cloudConfig CloudConfig
		if err := yaml.Unmarshal([]byte(cloudInitData.UserData), &cloudConfig); err != nil {
			return err
		}

		var hostDevices []kubevirtv1.HostDevice
		for i := range virtualMachineInstance.Spec.Domain.Devices.HostDevices {
			if !IsHostDevice(virtualMachineInstance.Spec.Domain.Devices.HostDevices[i]) {
				hostDevices = append(hostDevices, virtualMachineInstance.Spec.Domain.Devices.HostDevices[i])
			}
		}
		var index int
		for resourceName, resourceQuantity := range cloudConfig.InAccel {
			for range make([]interface{}, resourceQuantity.Value()) {
				hostDevices = append(hostDevices, HostDevice(string(resourceName), index))

				index++
			}
		}
		virtualMachineInstance.Spec.Domain.Devices.HostDevices = hostDevices
	}

	return nil
}
