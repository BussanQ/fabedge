package agent

import (
	"context"
	"net"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/fabedge/fabedge/pkg/common/constants"
	"github.com/fabedge/fabedge/pkg/operator/allocator"
	storepkg "github.com/fabedge/fabedge/pkg/operator/store"
	"github.com/fabedge/fabedge/pkg/operator/types"
)

type podCIDRHandler interface {
	Do(ctx context.Context, node corev1.Node) error
	Undo(ctx context.Context, nodeName string) error
}

var _ podCIDRHandler = &allocatablePodCIDRHandler{}
type allocatablePodCIDRHandler struct {
	client      client.Client
	allocator   allocator.Interface
	store       storepkg.Interface
	newEndpoint types.NewEndpointFunc
	log         logr.Logger
}

func (handler *allocatablePodCIDRHandler) Do(ctx context.Context, node corev1.Node) error {
	currentEndpoint := handler.newEndpoint(node)

	if !handler.isValidSubnets(currentEndpoint.Subnets) {
		if err := handler.allocateSubnet(ctx, node); err != nil {
			return err
		}
	} else {
		handler.store.SaveEndpoint(currentEndpoint)
	}

	return nil
}

func (handler *allocatablePodCIDRHandler) isValidSubnets(cidrs []string) bool {
	for _, cidr := range cidrs {
		_, subnet, err := net.ParseCIDR(cidr)
		if err != nil {
			return false
		}

		if !handler.allocator.Contains(*subnet) {
			return false
		}
	}

	return true
}

func (handler *allocatablePodCIDRHandler) allocateSubnet(ctx context.Context, node corev1.Node) error {
	log := handler.log.WithValues("nodeName", node.Name)

	log.V(5).Info("this node need subnet allocation")
	subnet, err := handler.allocator.GetFreeSubnetBlock(node.Name)
	if err != nil {
		log.Error(err, "failed to allocate subnet for node")
		return err
	}

	log = log.WithValues("subnet", subnet.String())
	log.V(5).Info("an subnet is allocated to node")

	if node.Annotations == nil {
		node.Annotations = map[string]string{}
	}
	// for now, we just supply one subnet allocation
	node.Annotations[constants.KeyPodSubnets] = subnet.String()

	err = handler.client.Update(ctx, &node)
	if err != nil {
		log.Error(err, "failed to record node subnet allocation")

		handler.allocator.Reclaim(*subnet)
		log.V(5).Info("subnet is reclaimed")
		return err
	}

	handler.store.SaveEndpoint(handler.newEndpoint(node))
	return nil
}

func (handler *allocatablePodCIDRHandler)  Undo(ctx context.Context, nodeName string) error {
	log := handler.log.WithValues("nodeName", nodeName)

	ep, ok := handler.store.GetEndpoint(nodeName)
	if !ok {
		return nil
	}
	handler.store.DeleteEndpoint(nodeName)
	log.V(5).Info("endpoint is delete from store", "endpoint", ep)

	for _, sn := range ep.Subnets {
		_, subnet, err := net.ParseCIDR(sn)
		if err != nil {
			log.Error(err, "invalid subnet, skip reclaiming subnets")
			continue
		}
		handler.allocator.Reclaim(*subnet)
		log.V(5).Info("subnet is reclaimed", "subnet", subnet)
	}

	return nil
}


var _ podCIDRHandler = &rawPodCIDRHandler{}
type rawPodCIDRHandler struct {
	client      client.Client
	allocator   allocator.Interface
	store       storepkg.Interface
	newEndpoint types.NewEndpointFunc
	log         logr.Logger
}

func (handler *rawPodCIDRHandler) Do(ctx context.Context, node corev1.Node) error {
	endpoint := handler.newEndpoint(node)
	handler.store.SaveEndpoint(endpoint)
	return nil
}

func (handler *rawPodCIDRHandler) Undo(ctx context.Context, nodeName string) error {
	return nil
}