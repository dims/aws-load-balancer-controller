package eventhandlers

import (
	"context"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/workqueue"
	elbv2api "sigs.k8s.io/aws-load-balancer-controller/apis/elbv2/v1beta1"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/backend"
	"sigs.k8s.io/aws-load-balancer-controller/pkg/k8s"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// NewEnqueueRequestsForNodeEvent constructs new enqueueRequestsForNodeEvent.
func NewEnqueueRequestsForNodeEvent(k8sClient client.Client, logger logr.Logger) handler.EventHandler {
	return &enqueueRequestsForNodeEvent{
		k8sClient: k8sClient,
		logger:    logger,
	}
}

type enqueueRequestsForNodeEvent struct {
	k8sClient client.Client
	logger    logr.Logger
}

// Create is called in response to an create event - e.g. Pod Creation.
func (h *enqueueRequestsForNodeEvent) Create(e event.CreateEvent, queue workqueue.RateLimitingInterface) {
	nodeNew := e.Object.(*corev1.Node)
	h.enqueueImpactedTargetGroupBindings(queue, nil, nodeNew)
}

// Update is called in response to an update event -  e.g. Pod Updated.
func (h *enqueueRequestsForNodeEvent) Update(e event.UpdateEvent, queue workqueue.RateLimitingInterface) {
	nodeOld := e.ObjectOld.(*corev1.Node)
	nodeNew := e.ObjectNew.(*corev1.Node)
	h.enqueueImpactedTargetGroupBindings(queue, nodeOld, nodeNew)
}

// Delete is called in response to a delete event - e.g. Pod Deleted.
func (h *enqueueRequestsForNodeEvent) Delete(e event.DeleteEvent, queue workqueue.RateLimitingInterface) {
	nodeOld := e.Object.(*corev1.Node)
	h.enqueueImpactedTargetGroupBindings(queue, nodeOld, nil)
}

// Generic is called in response to an event of an unknown type or a synthetic event triggered as a cron or
// external trigger request - e.g. reconcile AutoScaling, or a WebHook.
func (h *enqueueRequestsForNodeEvent) Generic(e event.GenericEvent, queue workqueue.RateLimitingInterface) {
	// nothing to do here
}

// enqueueImpactedEndpointBindings will enqueue all impacted TargetGroupBindings for node events.
func (h *enqueueRequestsForNodeEvent) enqueueImpactedTargetGroupBindings(queue workqueue.RateLimitingInterface, nodeOld *corev1.Node, nodeNew *corev1.Node) {
	var nodeKey types.NamespacedName
	nodeOldIsReady := false
	nodeNewIsReady := false
	if nodeOld != nil {
		nodeKey = k8s.NamespacedName(nodeOld)
		nodeOldIsReady = k8s.IsNodeSuitableAsTrafficProxy(nodeOld)
	}
	if nodeNew != nil {
		nodeKey = k8s.NamespacedName(nodeNew)
		nodeNewIsReady = k8s.IsNodeSuitableAsTrafficProxy(nodeNew)
	}

	tgbList := &elbv2api.TargetGroupBindingList{}
	if err := h.k8sClient.List(context.Background(), tgbList); err != nil {
		h.logger.Error(err, "failed to fetch targetGroupBindings")
		return
	}

	for _, tgb := range tgbList.Items {
		if tgb.Spec.TargetType == nil || (*tgb.Spec.TargetType) != elbv2api.TargetTypeInstance {
			continue
		}

		nodeSelector := backend.GetTrafficProxyNodeSelector(&tgb)

		nodeOldIsTrafficProxy := false
		nodeNewIsTrafficProxy := false
		if nodeOld != nil {
			nodeOldIsTrafficProxy = nodeOldIsReady && nodeSelector.Matches(labels.Set(nodeOld.Labels))
		}
		if nodeNew != nil {
			nodeNewIsTrafficProxy = nodeNewIsReady && nodeSelector.Matches(labels.Set(nodeNew.Labels))
		}

		if nodeOldIsTrafficProxy != nodeNewIsTrafficProxy {
			h.logger.V(1).Info("enqueue targetGroupBinding for node event",
				"node", nodeKey,
				"targetGroupBinding", k8s.NamespacedName(&tgb),
			)
			queue.Add(reconcile.Request{
				NamespacedName: types.NamespacedName{
					Namespace: tgb.Namespace,
					Name:      tgb.Name,
				},
			})
		}
	}
}
