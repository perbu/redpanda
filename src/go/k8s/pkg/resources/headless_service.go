// Copyright 2021 Vectorized, Inc.
//
// Use of this software is governed by the Business Source License
// included in the file licenses/BSL.md
//
// As of the Change Date specified in that file, in accordance with
// the Business Source License, use of this software will be governed
// by the Apache License, Version 2.0

package resources

import (
	"context"
	"fmt"

	"github.com/go-logr/logr"
	redpandav1alpha1 "github.com/vectorizedio/redpanda/src/go/k8s/apis/redpanda/v1alpha1"
	"github.com/vectorizedio/redpanda/src/go/k8s/pkg/labels"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var _ Resource = &HeadlessServiceResource{}

const (
	externalDNSHostname  = "external-dns.alpha.kubernetes.io/hostname"
	externalDNSUseHostIP = "external-dns.alpha.kubernetes.io/use-external-host-ip"
)

// HeadlessServiceResource is part of the reconciliation of redpanda.vectorized.io CRD
// focusing on the internal connectivity management of redpanda cluster
type HeadlessServiceResource struct {
	k8sclient.Client
	scheme       *runtime.Scheme
	pandaCluster *redpandav1alpha1.Cluster
	svcPorts     []NamedServicePort
	logger       logr.Logger
}

// NewHeadlessService creates HeadlessServiceResource
func NewHeadlessService(
	client k8sclient.Client,
	pandaCluster *redpandav1alpha1.Cluster,
	scheme *runtime.Scheme,
	svcPorts []NamedServicePort,
	logger logr.Logger,
) *HeadlessServiceResource {
	return &HeadlessServiceResource{
		client,
		scheme,
		pandaCluster,
		svcPorts,
		logger.WithValues(
			"Kind", serviceKind(),
			"ServiceType", corev1.ServiceTypeClusterIP,
			"ClusterIP", corev1.ClusterIPNone,
		),
	}
}

// Ensure will manage kubernetes v1.Service for redpanda.vectorized.io custom resource
func (r *HeadlessServiceResource) Ensure(ctx context.Context) error {
	obj, err := r.obj()
	if err != nil {
		return fmt.Errorf("unable to construct object: %w", err)
	}
	_, err = CreateIfNotExists(ctx, r, obj, r.logger)
	return err
}

// obj returns resource managed client.Object
func (r *HeadlessServiceResource) obj() (k8sclient.Object, error) {
	ports := make([]corev1.ServicePort, 0, len(r.svcPorts))
	for _, svcPort := range r.svcPorts {
		ports = append(ports, corev1.ServicePort{
			Name:       svcPort.Name,
			Protocol:   corev1.ProtocolTCP,
			Port:       int32(svcPort.Port),
			TargetPort: intstr.FromInt(svcPort.Port),
		})
	}

	objLabels := labels.ForCluster(r.pandaCluster)
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:   r.Key().Namespace,
			Name:        r.Key().Name,
			Labels:      objLabels,
			Annotations: r.getAnnotation(),
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		Spec: corev1.ServiceSpec{
			Type:      corev1.ServiceTypeClusterIP,
			ClusterIP: corev1.ClusterIPNone,
			Ports:     ports,
			Selector:  objLabels.AsAPISelector().MatchLabels,
		},
	}

	err := controllerutil.SetControllerReference(r.pandaCluster, svc, r.scheme)
	if err != nil {
		return nil, err
	}

	return svc, nil
}

// Key returns namespace/name object that is used to identify object.
// For reference please visit types.NamespacedName docs in k8s.io/apimachinery
func (r *HeadlessServiceResource) Key() types.NamespacedName {
	return types.NamespacedName{Name: r.pandaCluster.Name, Namespace: r.pandaCluster.Namespace}
}

func serviceKind() string {
	var svc corev1.Service
	return svc.Kind
}

// HeadlessServiceFQDN returns fully qualified domain name for headless service.
// It can be used to communicate between namespaces if the network policy
// allows it.
func (r *HeadlessServiceResource) HeadlessServiceFQDN() string {
	// TODO Retrieve cluster domain dynamically and remove hardcoded cluster.local
	return fmt.Sprintf("%s%c%s.svc.cluster.local.",
		r.Key().Name,
		'.',
		r.Key().Namespace)
}

func (r *HeadlessServiceResource) getAnnotation() map[string]string {
	if !r.pandaCluster.Spec.ExternalConnectivity.Enabled && r.pandaCluster.Spec.ExternalConnectivity.Subdomain == "" {
		return nil
	}

	return map[string]string{
		externalDNSHostname: r.pandaCluster.Spec.ExternalConnectivity.Subdomain,
		// This annotation comes from the not merged feature
		// https://github.com/kubernetes-sigs/external-dns/pull/1391
		externalDNSUseHostIP: "true",
	}
}
