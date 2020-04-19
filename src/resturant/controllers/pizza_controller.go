/*


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
	"strconv"
	"time"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	kbatch "k8s.io/api/batch/v1"

	resturantv1 "resturant/api/v1"
)

var (
	jobOwnerKey = ".metadata.controller"
	apiGVStr    = resturantv1.GroupVersion.String()
)

// PizzaReconciler reconciles a Pizza object
type PizzaReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=resturant.foodie.io,resources=pizzas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=resturant.foodie.io,resources=pizzas/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=resturant,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=resturant,resources=jobs/status,verbs=get

func (r *PizzaReconciler) Reconcile(req ctrl.Request) (ctrl.Result, error) {
	ctx := context.Background()
	log := r.Log.WithValues("pizza", req.NamespacedName)

	var pizza resturantv1.Pizza
	if err := r.Get(ctx, req.NamespacedName, &pizza); err != nil {
		log.Error(err, "unable to fetch Pizza")
		// we'll ignore not-found errors, since they can't be fixed by an immediate
		// requeue (we'll need to wait for a new notification), and we can get them
		// on deleted requests.
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	var childJobs kbatch.JobList
	if err := r.List(ctx, &childJobs, client.InNamespace(req.Namespace), client.MatchingFields{jobOwnerKey: req.Name}); err != nil {
		log.Info("Empty ChildList" + req.Name)
		return ctrl.Result{}, err
	}

	isJobFinished := func(job *kbatch.Job) (bool, kbatch.JobConditionType) {
		for _, c := range job.Status.Conditions {
			if (c.Type == kbatch.JobComplete || c.Type == kbatch.JobFailed) && c.Status == corev1.ConditionTrue {
				return true, c.Type
			}
		}
		return false, ""
	}

	if len(childJobs.Items) > 0 {
		log.Info(strconv.Itoa(len(childJobs.Items)))

		if stat, _ := isJobFinished(&childJobs.Items[0]); stat {

			pizza.Status.Price = 123
			pizza.Status.Status = "Ready to Pickup"
		} else {
			pizza.Status.Status = "Preparingg Pizza"
		}

		if err := r.Status().Update(ctx, &pizza); err != nil {
			log.Error(err, "unable to update Pizza status")
			return ctrl.Result{}, err
		}

	} else {

		job, err := r.PrepareSpec(&pizza)

		if err != nil {
			log.Error(err, "unable to construct job from template")
			return ctrl.Result{}, err
		}

		if err := r.Create(ctx, job); err != nil {
			log.Error(err, "unable to create Job")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil

}

func (r *PizzaReconciler) SetupWithManager(mgr ctrl.Manager) error {

	if err := mgr.GetFieldIndexer().IndexField(&kbatch.Job{}, jobOwnerKey, func(rawObj runtime.Object) []string {
		// grab the job object, extract the owner...
		job := rawObj.(*kbatch.Job)
		owner := metav1.GetControllerOf(job)
		if owner == nil {
			return nil
		}
		// ...make sure it's a CronJob...
		if owner.APIVersion != apiGVStr || owner.Kind != "Pizza" {
			return nil
		}

		// ...and if so, return it
		return []string{owner.Name}
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&resturantv1.Pizza{}).
		Owns(&kbatch.Job{}).
		Complete(r)
}

func (r *PizzaReconciler) PrepareSpec(pizza *resturantv1.Pizza) (*kbatch.Job, error) {
	name := fmt.Sprintf("%s-%d", pizza.Name, time.Now().Unix())

	job := &kbatch.Job{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      make(map[string]string),
			Annotations: make(map[string]string),
			Name:        name,
			Namespace:   pizza.Namespace,
		},
		Spec: *pizza.Spec.JobTemplate.Spec.DeepCopy(),
	}

	if err := ctrl.SetControllerReference(pizza, job, r.Scheme); err != nil {
		return nil, err
	}

	return job, nil
}
