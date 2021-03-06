/*
Copyright 2021 The RamenDR authors.

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

package controllers

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/prometheus/common/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	ramendrv1alpha1 "github.com/ramendr/ramen/api/v1alpha1"
)

// VolumeReplicationGroupReconciler reconciles a VolumeReplicationGroup object
type VolumeReplicationGroupReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func newPredicateFunc() predicate.Funcs {
	// predicate functions send reconcile requests for create and delete events.
	// For them the filtering of whether the pvc belongs to the any of the
	// VolumeReplicationGroup CRs and identifying such a CR is done in the
	// map function by comparing namespaces and labels.
	// But for update of pvc, the reconcile request should be sent only for
	// spec changes. Do that comparison here.
	pvcPredicate := predicate.Funcs{
		// This predicate function can be removed as the only thing it is
		// doing currently is to log the pvc creation event that is received.
		// Even without this predicate function, by default the reconcile
		// request would be sent (i.e. equivalent of returning true from here).
		// However having a log here will be useful for debugging purposes where
		// one can verify and distinguish between below 2 events.
		// 1) pvc creation for the application for which VRG CR exists.
		//    (i.e. application is disaster protected). For this reconcile
		//    logic will be triggered.
		// 2) pvc creation for the application for which VRG CR does not exist
		//    (i.e. application is not disaster protected). For this reconcile
		//    logic is not triggered.
		CreateFunc: func(e event.CreateEvent) bool {
			log.Debug("create event from pvc")

			return true
		},
		UpdateFunc: func(e event.UpdateEvent) bool {
			oldPVC, ok := e.ObjectOld.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Error("Update: failed to read old PVC, not reconciling")

				return false
			}
			newPVC, ok := e.ObjectNew.DeepCopyObject().(*corev1.PersistentVolumeClaim)
			if !ok {
				log.Error("Update: failed to read new PVC, not reconciling")

				return false
			}
			log.Debug("Update event from pvc")

			return !reflect.DeepEqual(oldPVC.Spec, newPVC.Spec)
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			log.Info("delete event from pvc")

			// return false. Because, for delete event on a pvc, it is
			// the finalizer which would actually delete the backed up
			// pv and the VolumeReplication CR created for that pvc.
			// In future if there arises a need to unconditionally
			// do a reconcile, return true. If there is a need to do
			// reconcile for certain conditions, then add the necessary
			// checks here.
			return false
		},
	}

	return pvcPredicate
}

func filterPVC(mgr manager.Manager, pvc *corev1.PersistentVolumeClaim) []reconcile.Request {
	req := []reconcile.Request{}

	var vrgs ramendrv1alpha1.VolumeReplicationGroupList

	listOptions := []client.ListOption{
		client.InNamespace(pvc.Namespace),
	}

	// decide if reconcile request needs to be sent to the
	// corresponding VolumeReplicationGroup CR by:
	// - whether there is a VolumeReplicationGroup CR in the namespace
	//   to which the the pvc belongs to.
	// - whether the labels on pvc match the label selectors from
	//    VolumeReplicationGroup CR.
	err := mgr.GetClient().List(context.TODO(), &vrgs, listOptions...)
	if err != nil {
		log.Errorf("failed to get list of VolumeReplicationGroup CRs")

		return []reconcile.Request{}
	}

	for _, vrg := range vrgs.Items {
		vrgLabelSelector := vrg.Spec.ApplicationLabels
		selector, err := metav1.LabelSelectorAsSelector(&vrgLabelSelector)
		// continue if we fail to get the labels for this object hoping
		// that pvc might actually belong to  some other vrg instead of
		// this. If not found, then reconcile request would not be sent
		if err != nil {
			log.Error("failed to get the label selector as selector")

			continue
		}

		if selector.Matches(labels.Set(pvc.GetLabels())) {
			log.Info("found volume replication group that belongs to namespace with matching labels: ", pvc.Namespace)

			req = append(req, reconcile.Request{NamespacedName: types.NamespacedName{Name: vrg.Name, Namespace: vrg.Namespace}})
		}
	}

	return req
}

// nolint: lll // disabling line length linter
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=ramendr.openshift.io,resources=volumereplicationgroups/finalizers,verbs=update
// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=persistentvolumes,verbs=get;list;watch

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the VolumeReplicationGroup object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.7.0/pkg/reconcile
func (v *VolumeReplicationGroupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = v.Log.WithValues("volumereplicationgroup reconciler", req.NamespacedName)

	// Fetch the VolumeReplicationGroup instance
	volRepGroup := &ramendrv1alpha1.VolumeReplicationGroup{}
	if err := v.Get(ctx, req.NamespacedName, volRepGroup); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("VolumeReplicationGroup resource not found. Ignoring since object must have been deleted: ",
				req.NamespacedName)

			return ctrl.Result{}, nil
		}

		// Error reading the object - requeue the request.
		log.Error(err, "Failed to get VolumeReplicationGroup")

		return ctrl.Result{}, fmt.Errorf("failed to reconcile VolumeReplicationGroup %w", err)
	}

	log.Info("Processing VolumeReplicationGroup ", volRepGroup.Spec, " in ns: ", req.NamespacedName)

	pvcList, err := v.HandlePersistentVolumeClaims(ctx, volRepGroup)
	if err != nil {
		log.Error("Handling of Persistent Volume Claims of application failed: ", volRepGroup.Spec.ApplicationName)

		return ctrl.Result{}, err
	}

	err = v.HandlePersistentVolumes(ctx, pvcList)
	if err != nil {
		log.Error("Handling of Persistent Volumes for PVCs of application failed: ", volRepGroup.Spec.ApplicationName)

		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// HandlePersistentVolumeClaims handles PVCs in the VRG
func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumeClaims(
	ctx context.Context,
	volRepGroup *ramendrv1alpha1.VolumeReplicationGroup) (
	*corev1.PersistentVolumeClaimList,
	error) {
	labelSelector := volRepGroup.Spec.ApplicationLabels
	pvcList := &corev1.PersistentVolumeClaimList{}

	log.Info("Label Selector: ", labels.Set(labelSelector.MatchLabels))
	log.Info("Namespace: ", volRepGroup.Namespace)
	listOptions := []client.ListOption{
		client.InNamespace(volRepGroup.Namespace),
		client.MatchingLabels(labelSelector.MatchLabels),
	}

	if err := v.List(ctx, pvcList, listOptions...); err != nil {
		log.Error(err, "Failed to list PVC")

		return pvcList, fmt.Errorf("failed to list PVC %w", err)
	}

	log.Info("PVC List: ", pvcList)

	return pvcList, nil
}

// HandlePersistentVolumes handles bound PVs
func (v *VolumeReplicationGroupReconciler) HandlePersistentVolumes(
	ctx context.Context,
	pvcList *corev1.PersistentVolumeClaimList) error {
	template := "%-42s%-42s%-8s%-10s%-8s\n"

	log.Info("---------- PVs ----------")
	log.Infof(template, "NAME", "CLAIM REF", "STATUS", "CAPACITY", "RECLAIM POLICY")

	var capacity resource.Quantity

	for _, pvc := range pvcList.Items {
		// if the PVC is not bound yet, dont proceed.
		if pvc.Status.Phase != corev1.ClaimBound {
			return fmt.Errorf("PVC is not yet bound status: %v", pvc.Status.Phase)
		}

		volumeName := pvc.Spec.VolumeName
		pv := &corev1.PersistentVolume{}
		pvObjectKey := client.ObjectKey{
			Name: pvc.Spec.VolumeName,
		}

		if err := v.Get(ctx, pvObjectKey, pv); err != nil {
			if errors.IsNotFound(err) {
				// Request object not found, could have been deleted after reconcile request.
				// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
				// Return and don't requeue
				log.Info("PersistentVolume resource not found. Ignoring since object must be deleted", volumeName)

				continue
			}

			return fmt.Errorf("unable to PV %v (err: %w)", pvObjectKey, err)
		}

		claimRefUID := ""
		if pv.Spec.ClaimRef != nil {
			claimRefUID += pv.Spec.ClaimRef.Namespace
			claimRefUID += "/"
			claimRefUID += pv.Spec.ClaimRef.Name
		}

		reclaimPolicyStr := string(pv.Spec.PersistentVolumeReclaimPolicy)

		quant := pv.Spec.Capacity[corev1.ResourceStorage]
		capacity.Add(quant)
		log.Infof(template, pv.Name, claimRefUID, string(pv.Status.Phase), quant.String(),
			reclaimPolicyStr)
	}

	log.Info("Total capacity Used: ", capacity.String())

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (v *VolumeReplicationGroupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	pvcPredicate := newPredicateFunc()
	pvcMapFun := handler.EnqueueRequestsFromMapFunc(handler.MapFunc(func(obj client.Object) []reconcile.Request {
		pvc, ok := obj.(*corev1.PersistentVolumeClaim)
		if !ok {
			// Not a pvc, returning empty
			log.Errorf("pvc handler received non-pvc")

			return []reconcile.Request{}
		}

		log.Info("pvc Namespace: ", pvc.Namespace)
		req := filterPVC(mgr, pvc)

		return req
	}))

	log.Info("Adding VRG and PVC controller")

	return ctrl.NewControllerManagedBy(mgr).
		For(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Watches(&source.Kind{Type: &corev1.PersistentVolumeClaim{}}, pvcMapFun, builder.WithPredicates(pvcPredicate)).
		// The actual thing that the controller owns is
		// the VolumeReplication CR. Change the below
		// line when VolumeReplication CR is ready.
		Owns(&ramendrv1alpha1.VolumeReplicationGroup{}).
		Complete(v)
}
