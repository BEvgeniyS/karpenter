/*
Copyright The Kubernetes Authors.

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

package hash

import (
	"context"
	"fmt"
	"strings"

	"github.com/samber/lo"
	"go.uber.org/multierr"
	"k8s.io/apimachinery/pkg/api/equality"
	controllerruntime "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/patrickmn/go-cache"

	v1 "sigs.k8s.io/karpenter/pkg/apis/v1"
	"sigs.k8s.io/karpenter/pkg/operator/injection"
)

// Controller is hash controller that constructs a hash based on the fields that are considered for static drift.
// The hash is placed in the metadata for increased observability and should be found on each object.
type Controller struct {
	kubeClient client.Client
	cache      *cache.Cache
}

func NewController(kubeClient client.Client, sharedCache *cache.Cache) *Controller {
	return &Controller{
		kubeClient: kubeClient,
		cache:      sharedCache,
	}
}

// Reconcile the resource
func (c *Controller) Reconcile(ctx context.Context, np *v1.NodePool) (reconcile.Result, error) {
	ctx = injection.WithControllerName(ctx, "nodepool.hash")

	stored := np.DeepCopy()

	if np.Annotations[v1.NodePoolHashVersionAnnotationKey] != v1.NodePoolHashVersion {
		if err := c.updateNodeClaimHash(ctx, np); err != nil {
			return reconcile.Result{}, err
		}
	}
	np.Annotations = lo.Assign(np.Annotations, map[string]string{
		v1.NodePoolHashAnnotationKey:        np.Hash(),
		v1.NodePoolHashVersionAnnotationKey: v1.NodePoolHashVersion,
	})

	if !equality.Semantic.DeepEqual(stored, np) {
		// Clear relevant allocatable cache if the hash has changed
		for cacheKey := range c.cache.Items() {
			if strings.HasPrefix(cacheKey, fmt.Sprintf("allocatableCache;%s;", np.Name)) {
				c.cache.Delete(cacheKey)
				log.FromContext(ctx).WithValues("NodePool", np.Name).Info("Cleared allocatable cache")
			}
		}
		if err := c.kubeClient.Patch(ctx, np, client.MergeFrom(stored)); err != nil {
			return reconcile.Result{}, client.IgnoreNotFound(err)
		}
	}
	return reconcile.Result{}, nil
}

func (c *Controller) Register(_ context.Context, m manager.Manager) error {
	return controllerruntime.NewControllerManagedBy(m).
		Named("nodepool.hash").
		For(&v1.NodePool{}).
		WithOptions(controller.Options{MaxConcurrentReconciles: 10}).
		Complete(reconcile.AsReconciler(m.GetClient(), c))
}

// Updating `nodepool-hash-version` annotation inside the karpenter controller means a breaking change has been made to the hash calculation.
// The `nodepool-hash` annotation on the NodePool will be updated, due to the breaking change, making the `nodepool-hash` on the NodeClaim different from
// NodePool. Since, we cannot rely on the `nodepool-hash` on the NodeClaims, due to the breaking change, we will need to re-calculate the hash and update the annotation.
// For more information on the Drift Hash Versioning: https://github.com/kubernetes-sigs/karpenter/blob/main/designs/drift-hash-versioning.md
func (c *Controller) updateNodeClaimHash(ctx context.Context, np *v1.NodePool) error {
	ncList := &v1.NodeClaimList{}
	if err := c.kubeClient.List(ctx, ncList, client.MatchingLabels(map[string]string{v1.NodePoolLabelKey: np.Name})); err != nil {
		return err
	}

	errs := make([]error, len(ncList.Items))
	for i := range ncList.Items {
		nc := ncList.Items[i]
		stored := nc.DeepCopy()

		if nc.Annotations[v1.NodePoolHashVersionAnnotationKey] != v1.NodePoolHashVersion {
			nc.Annotations = lo.Assign(nc.Annotations, map[string]string{
				v1.NodePoolHashVersionAnnotationKey: v1.NodePoolHashVersion,
			})

			// Any NodeClaim that is already drifted will remain drifted if the karpenter.sh/nodepool-hash-version doesn't match
			// Since the hashing mechanism has changed we will not be able to determine if the drifted status of the NodeClaim has changed
			if nc.StatusConditions().Get(v1.ConditionTypeDrifted) == nil {
				nc.Annotations = lo.Assign(nc.Annotations, map[string]string{
					v1.NodePoolHashAnnotationKey: np.Hash(),
				})
			}

			if !equality.Semantic.DeepEqual(stored, nc) {
				if err := c.kubeClient.Patch(ctx, &nc, client.MergeFrom(stored)); err != nil {
					errs[i] = client.IgnoreNotFound(err)
				}
			}
		}
	}

	return multierr.Combine(errs...)
}
