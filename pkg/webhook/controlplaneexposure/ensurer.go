// Copyright (c) 2019 SAP SE or an SAP affiliate company. All rights reserved. This file is licensed under the Apache Software License, v. 2 except as noted otherwise in the LICENSE file
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controlplaneexposure

import (
	"context"
	"fmt"

	"github.com/gardener/gardener-extension-provider-equinix-metal/pkg/apis/config"

	druidv1alpha1 "github.com/gardener/etcd-druid/api/v1alpha1"
	extensionswebhook "github.com/gardener/gardener/extensions/pkg/webhook"
	gcontext "github.com/gardener/gardener/extensions/pkg/webhook/context"
	"github.com/gardener/gardener/extensions/pkg/webhook/controlplane/genericmutator"
	v1beta1constants "github.com/gardener/gardener/pkg/apis/core/v1beta1/constants"
	v1beta1helper "github.com/gardener/gardener/pkg/apis/core/v1beta1/helper"
	kutil "github.com/gardener/gardener/pkg/utils/kubernetes"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// NewEnsurer creates a new controlplaneexposure ensurer.
func NewEnsurer(etcdStorage *config.ETCDStorage, logger logr.Logger) genericmutator.Ensurer {
	return &ensurer{
		etcdStorage: etcdStorage,
		logger:      logger.WithName("equinix-metal-controlplaneexposure-ensurer"),
	}
}

type ensurer struct {
	genericmutator.NoopEnsurer
	etcdStorage *config.ETCDStorage
	client      client.Client
	logger      logr.Logger
}

// InjectClient injects the given client into the ensurer.
func (e *ensurer) InjectClient(client client.Client) error {
	e.client = client
	return nil
}

// EnsureKubeAPIServerDeployment ensures that the kube-apiserver deployment conforms to the provider requirements.
func (e *ensurer) EnsureKubeAPIServerDeployment(ctx context.Context, gctx gcontext.GardenContext, new, old *appsv1.Deployment) error {
	if v1beta1helper.IsAPIServerExposureManaged(new) {
		return nil
	}

	c := extensionswebhook.ContainerWithName(new.Spec.Template.Spec.Containers, "kube-apiserver")
	if c == nil {
		return nil
	}

	ip, err := e.getServiceFirstLoadBalancerIP(ctx, new.Namespace)
	if err != nil {
		return fmt.Errorf("getting API Service first LoadBalancer IP: %w", err)
	}

	c.Command = extensionswebhook.EnsureStringWithPrefix(c.Command, "--advertise-address=", ip)

	return nil
}

// EnsureETCD ensures that the etcd conform to the provider requirements.
func (e *ensurer) EnsureETCD(ctx context.Context, gctx gcontext.GardenContext, new, old *druidv1alpha1.Etcd) error {
	capacity := resource.MustParse("10Gi")
	class := ""

	if new.Name == v1beta1constants.ETCDMain && e.etcdStorage != nil {
		if e.etcdStorage.Capacity != nil {
			capacity = *e.etcdStorage.Capacity
		}
		if e.etcdStorage.ClassName != nil {
			class = *e.etcdStorage.ClassName
		}
	}

	new.Spec.StorageClass = &class
	new.Spec.StorageCapacity = &capacity

	return nil
}

// getServiceFirstLoadBalancerIP is equivalent of kutil.GetLoadBalancerIngress, but it ignores Hostname field
// on load balancer status, as --advertise-address flag for kube-apiserver does not accept hostnames.
func (e *ensurer) getServiceFirstLoadBalancerIP(ctx context.Context, ns string) (string, error) {
	service := &corev1.Service{}
	if err := e.client.Get(ctx, kutil.Key(ns, v1beta1constants.DeploymentNameKubeAPIServer), service); err != nil {
		return "", fmt.Errorf("getting kube-apiserver service: %w", err)
	}

	serviceStatusIngress := service.Status.LoadBalancer.Ingress
	length := len(serviceStatusIngress)

	if length == 0 {
		return "", fmt.Errorf("`.status.loadBalancer.ingress[]` has no elements yet, i.e. external load balancer has not been created")
	}

	ip := serviceStatusIngress[length-1].IP
	if ip == "" {
		return "", fmt.Errorf("`.status.loadBalancer.ingress[-1]` has no IP address set yet")
	}

	return ip, nil
}
