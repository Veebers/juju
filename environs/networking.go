// Copyright 2015 Canonical Ltd.
// Licensed under the AGPLv3, see LICENCE file for details.

package environs

import (
	"github.com/juju/utils/featureflag"

	"github.com/juju/juju/feature"
	"github.com/juju/juju/instance"
	"github.com/juju/juju/network"
	"github.com/juju/juju/provider"
)

// SupportsNetworking is a convenience helper to check if an environment
// supports networking. It returns an interface containing Environ and
// Networking in this case.
var SupportsNetworking = supportsNetworking

// Networking interface defines methods that environments
// with networking capabilities must implement.
type Networking interface {
	// AllocateAddress requests a specific address to be allocated for the given
	// instance on the given subnet, using the specified macAddress and
	// hostnameSuffix. If addr is empty, this is interpreted as an output
	// argument, which will contain the allocated address. Otherwise, addr must
	// be non-empty and will be allocated as specified, if possible.
	AllocateAddress(instId instance.Id, subnetId network.Id, addr *network.Address, macAddress, hostnameSuffix string) error

	// ReleaseAddress releases a specific address previously allocated with
	// AllocateAddress.
	ReleaseAddress(instId instance.Id, subnetId network.Id, addr network.Address, macAddress, hostname string) error

	// Subnets returns basic information about subnets known
	// by the provider for the environment.
	Subnets(inst instance.Id, subnetIds []network.Id) ([]network.SubnetInfo, error)

	// NetworkInterfaces requests information about the network
	// interfaces on the given instance.
	NetworkInterfaces(instId instance.Id) ([]network.InterfaceInfo, error)

	// SupportsAddressAllocation returns whether the given subnetId
	// supports static IP address allocation using AllocateAddress and
	// ReleaseAddress. If subnetId is network.AnySubnet, the provider
	// can decide whether it can return true or a false and an error
	// (e.g. "subnetId must be set").
	SupportsAddressAllocation(subnetId network.Id) (bool, error)

	// SupportsSpaces returns whether the current environment supports
	// spaces. The returned error satisfies errors.IsNotSupported(),
	// unless a general API failure occurs.
	SupportsSpaces() (bool, error)

	// SupportsSpaceDiscovery returns whether the current environment
	// supports discovering spaces from the provider. The returned error
	// satisfies errors.IsNotSupported(), unless a general API failure occurs.
	SupportsSpaceDiscovery() (bool, error)

	// Spaces returns a slice of network.SpaceInfo with info, including
	// details of all associated subnets, about all spaces known to the
	// provider that have subnets available.
	Spaces() ([]network.SpaceInfo, error)

	// AllocateContainerAddresses allocates a static address for each of the
	// container NICs in preparedInfo, hosted by the hostInstanceID. Returns the
	// network config including all allocated addresses on success.
	AllocateContainerAddresses(hostInstanceID instance.Id, preparedInfo []network.InterfaceInfo) ([]network.InterfaceInfo, error)
}

// NetworkingEnviron combines the standard Environ interface with the
// functionality for networking.
type NetworkingEnviron interface {
	// Environ represents a juju environment.
	Environ

	// Networking defines the methods of networking capable environments.
	Networking
}

func supportsNetworking(environ Environ) (NetworkingEnviron, bool) {
	ne, ok := environ.(NetworkingEnviron)
	return ne, ok
}

// AddressAllocationEnabled is a shortcut for checking if the AddressAllocation
// feature flag is enabled. The providerType is used to distinguish between MAAS
// and other providers that still support the legacy address allocation (EC2 and
// Dummy) until the support can be removed across the board.
func AddressAllocationEnabled(providerType string) bool {
	if providerType == provider.MAAS {
		return false
	}
	return featureflag.Enabled(feature.AddressAllocation)
}
