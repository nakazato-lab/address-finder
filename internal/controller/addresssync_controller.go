/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controller

import (
	"context"
	"fmt"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	ndnv1alpha1 "github.com/tryuuu/address-finder/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	requeueInterval = 10 * time.Second
	configMapKey    = "ADDRESS"
)

// AddressSyncReconciler reconciles a AddressSync object
type AddressSyncReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=ndn.tryu.dev,resources=addresssyncs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ndn.tryu.dev,resources=addresssyncs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ndn.tryu.dev,resources=addresssyncs/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the AddressSync object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.19.1/pkg/reconcile
func (r *AddressSyncReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var as ndnv1alpha1.AddressSync
	if err := r.Get(ctx, req.NamespacedName, &as); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// we got info about NFD pod
	pod, found, err := r.findWatchTargetPod(ctx, &as)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !found {
		logger.Info("watch target pod not ready, retrying later")
		return ctrl.Result{RequeueAfter: requeueInterval}, nil
	}

	port, err := extractPort(pod, as.Spec.Watch.PortName)
	if err != nil {
		logger.Error(err, "failed to extract port from watch target pod")
		return ctrl.Result{}, err
	}
	address := fmt.Sprintf("%s:%d", pod.Status.PodIP, port)

	if err := r.reconcileConfigMap(ctx, &as, address); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.updateStatus(ctx, &as, address); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// findWatchTargetPod finds the Pod matching as.Spec.Watch.Selector in the same namespace.
func (r *AddressSyncReconciler) findWatchTargetPod(ctx context.Context, as *ndnv1alpha1.AddressSync) (*corev1.Pod, bool, error) {
	logger := log.FromContext(ctx)

	sel, err := metav1.LabelSelectorAsSelector(&as.Spec.Watch.Selector)
	if err != nil {
		return nil, false, err
	}

	var podList corev1.PodList
	if err := r.List(ctx, &podList, client.InNamespace(as.Namespace), client.MatchingLabelsSelector{Selector: sel}); err != nil {
		return nil, false, err
	}
	if len(podList.Items) == 0 {
		return nil, false, nil
	}
	// currently, we suppose 1 NFD at cluster.
	if len(podList.Items) > 1 {
		logger.Info("multiple pods matched watch selector, using the first one", "count", len(podList.Items))
	}

	pod := podList.Items[0]
	if pod.Status.PodIP == "" {
		return nil, false, nil
	}

	return &pod, true, nil
}

func extractPort(pod *corev1.Pod, portName string) (int32, error) {
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Name == portName {
				return p.ContainerPort, nil
			}
		}
	}
	return 0, fmt.Errorf("no port named %q found on pod %s/%s", portName, pod.Namespace, pod.Name)
}

// reconcileConfigMap creates or updates the ConfigMap that stores the watched address.
func (r *AddressSyncReconciler) reconcileConfigMap(ctx context.Context, as *ndnv1alpha1.AddressSync, address string) error {
	logger := log.FromContext(ctx)

	configMapName := as.Name + "-config"
	var cm corev1.ConfigMap
	cmKey := client.ObjectKey{Namespace: as.Namespace, Name: configMapName}

	if err := r.Get(ctx, cmKey, &cm); err != nil {
		if !apierrors.IsNotFound(err) {
			logger.Error(err, "failed to get ConfigMap")
			return err
		}
		cm = corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: as.Namespace,
			},
			Data: map[string]string{configMapKey: address},
		}
		if err := r.Create(ctx, &cm); err != nil {
			logger.Error(err, "failed to create ConfigMap")
			return err
		}
		return nil
	}

	if cm.Data[configMapKey] == address {
		return nil
	}
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[configMapKey] = address
	if err := r.Update(ctx, &cm); err != nil {
		logger.Error(err, "failed to update ConfigMap")
		return err
	}
	return nil
}

// updateStatus updates as.Status.CurrentAddress if it has changed.
func (r *AddressSyncReconciler) updateStatus(ctx context.Context, as *ndnv1alpha1.AddressSync, address string) error {
	if as.Status.CurrentAddress == address {
		return nil
	}
	as.Status.CurrentAddress = address
	if err := r.Status().Update(ctx, as); err != nil {
		log.FromContext(ctx).Error(err, "failed to update AddressSync status")
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *AddressSyncReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&ndnv1alpha1.AddressSync{}).
		Watches(
			&corev1.Pod{}, // watch all event about pod
			handler.EnqueueRequestsFromMapFunc(r.findAddressSyncForPod), // called when a pod is changed
		).
		Named("addresssync").
		Complete(r)
}

// Determines which Reconcile requests to enqueue when a Pod changes.
// We'd want to compare against the old IP, but this function has no access to the previous state.
func (r *AddressSyncReconciler) findAddressSyncForPod(ctx context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}

	var asList ndnv1alpha1.AddressSyncList
	// List all AddressSyncs in the Pod's namespace.
	if err := r.List(ctx, &asList, client.InNamespace(pod.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list AddressSync while mapping pod event")
		return nil
	}

	var requests []reconcile.Request
	for _, as := range asList.Items {
		sel, err := metav1.LabelSelectorAsSelector(&as.Spec.Watch.Selector)
		if err != nil {
			continue
		}
		// If the Pod's labels match the AddressSync's selector, enqueue that AddressSync for reconciliation.
		if sel.Matches(labels.Set(pod.Labels)) {
			requests = append(requests, reconcile.Request{
				NamespacedName: client.ObjectKeyFromObject(&as),
			})
		}
	}
	return requests
}
