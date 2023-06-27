// Copyright 2022 Antrea Authors.
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

package azure

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/cenkalti/backoff/v4"
	"github.com/mohae/deepcopy"
	"k8s.io/apimachinery/pkg/types"

	crdv1alpha1 "antrea.io/nephe/apis/crd/v1alpha1"
	runtimev1alpha1 "antrea.io/nephe/apis/runtime/v1alpha1"
	"antrea.io/nephe/pkg/cloudprovider/plugins/internal"
)

type computeServiceConfig struct {
	account                types.NamespacedName
	nwIntfAPIClient        azureNwIntfWrapper
	nsgAPIClient           azureNsgWrapper
	asgAPIClient           azureAsgWrapper
	vnetAPIClient          azureVirtualNetworksWrapper
	resourceGraphAPIClient azureResourceGraphWrapper
	resourcesCache         *internal.CloudServiceResourcesCache
	inventoryStats         *internal.CloudServiceStats
	credentials            *azureAccountConfig
	computeFilters         map[string][]*string
	// selectors required for updating resource filters on account config update.
	selectors map[string]*crdv1alpha1.CloudEntitySelector
}

type computeResourcesCacheSnapshot struct {
	virtualMachines map[internal.InstanceID]*virtualMachineTable
	vnets           []armnetwork.VirtualNetwork
	vnetIDs         map[string]struct{}
	vnetPeers       map[string][][]string
}

func newComputeServiceConfig(account types.NamespacedName, service azureServiceClientCreateInterface,
	credentials *azureAccountConfig) (internal.CloudServiceInterface, error) {
	// create compute sdk api client
	nwIntfAPIClient, err := service.networkInterfaces(credentials.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("error creating compute sdk api client for account : %v, err: %v", account, err)
	}
	// create security-groups sdk api client
	securityGroupsAPIClient, err := service.securityGroups(credentials.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("error creating security-groups sdk api client for account : %v, err: %v", account, err)
	}
	// create application-security-groups sdk api client
	applicationSecurityGroupsAPIClient, err := service.applicationSecurityGroups(credentials.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("error creating application-security-groups sdk api client for account : %v, err: %v", account, err)
	}
	// create resource-graph sdk api client
	resourceGraphAPIClient, err := service.resourceGraph()
	if err != nil {
		return nil, fmt.Errorf("error creating resource-graph sdk api client for account : %v, err: %v", account, err)
	}

	// create virtual networks sdk api client
	vnetAPIClient, err := service.virtualNetworks(credentials.SubscriptionID)
	if err != nil {
		return nil, fmt.Errorf("error creating virtual networks sdk api client for account : %v, err: %v", account, err)
	}

	config := &computeServiceConfig{
		account:                account,
		nwIntfAPIClient:        nwIntfAPIClient,
		nsgAPIClient:           securityGroupsAPIClient,
		asgAPIClient:           applicationSecurityGroupsAPIClient,
		vnetAPIClient:          vnetAPIClient,
		resourceGraphAPIClient: resourceGraphAPIClient,
		resourcesCache:         &internal.CloudServiceResourcesCache{},
		inventoryStats:         &internal.CloudServiceStats{},
		credentials:            credentials,
		computeFilters:         make(map[string][]*string),
		selectors:              make(map[string]*crdv1alpha1.CloudEntitySelector),
	}
	return config, nil
}

func (computeCfg *computeServiceConfig) waitForInventoryInit(duration time.Duration) error {
	operation := func() error {
		done := computeCfg.inventoryStats.IsInventoryInitialized()
		if !done {
			return fmt.Errorf("inventory for account %v not initialized (waited %v duration)", computeCfg.account, duration)
		}
		return nil
	}

	b := backoff.NewExponentialBackOff()
	b.MaxElapsedTime = duration

	return backoff.Retry(operation, b)
}

// getCachedVirtualMachines returns virtualMachines from the cache for the subscription.
func (computeCfg *computeServiceConfig) getCachedVirtualMachines() []*virtualMachineTable {
	snapshot := computeCfg.resourcesCache.GetSnapshot()
	if snapshot == nil {
		azurePluginLogger().V(4).Info("Compute service cache snapshot nil", "type", providerType, "account", computeCfg.account)
		return []*virtualMachineTable{}
	}
	virtualMachines := snapshot.(*computeResourcesCacheSnapshot).virtualMachines
	instancesToReturn := make([]*virtualMachineTable, 0, len(virtualMachines))
	for _, virtualMachine := range virtualMachines {
		instancesToReturn = append(instancesToReturn, virtualMachine)
	}

	azurePluginLogger().V(1).Info("Cached vm instances", "account", computeCfg.account,
		"instances", len(instancesToReturn))
	return instancesToReturn
}

// getManagedVnetIDs returns vnetIDs of vnets containing managed vms.
func (computeCfg *computeServiceConfig) getManagedVnetIDs() map[string]struct{} {
	vnetIDsCopy := make(map[string]struct{})
	snapshot := computeCfg.resourcesCache.GetSnapshot()
	if snapshot == nil {
		azurePluginLogger().Info("Compute service cache snapshot nil", "type", providerType, "account", computeCfg.account)
		return vnetIDsCopy
	}
	vnetIDsSet := snapshot.(*computeResourcesCacheSnapshot).vnetIDs

	for vnetID := range vnetIDsSet {
		vnetIDsCopy[vnetID] = struct{}{}
	}

	return vnetIDsCopy
}

// getManagedVnets returns vnets containing managed vms.
func (computeCfg *computeServiceConfig) getManagedVnets() map[string]armnetwork.VirtualNetwork {
	vnetCopy := make(map[string]armnetwork.VirtualNetwork)
	snapshot := computeCfg.resourcesCache.GetSnapshot()
	if snapshot == nil {
		azurePluginLogger().Info("Compute service cache snapshot nil", "type", providerType, "account", computeCfg.account)
		return vnetCopy
	}

	for _, vnet := range snapshot.(*computeResourcesCacheSnapshot).vnets {
		vnetCopy[strings.ToLower(*vnet.ID)] = vnet
	}

	return vnetCopy
}

func (computeCfg *computeServiceConfig) getVnetPeers(vnetID string) [][]string {
	snapshot := computeCfg.resourcesCache.GetSnapshot()
	if snapshot == nil {
		azurePluginLogger().V(4).Info("Compute service cache snapshot nil", "type", providerType, "account", computeCfg.account)
		return nil
	}
	vnetPeersCopy := make([][]string, 0)
	if peers, ok := snapshot.(*computeResourcesCacheSnapshot).vnetPeers[vnetID]; ok {
		vnetPeersCopy = deepcopy.Copy(peers).([][]string)
	}
	return vnetPeersCopy
}

func (computeCfg *computeServiceConfig) getVirtualMachines() ([]*virtualMachineTable, error) {
	filters, hasFilters := computeCfg.getComputeResourceFilters()
	if !hasFilters {
		azurePluginLogger().V(1).Info("Fetching vm resources from cloud skipped",
			"account", computeCfg.account, "resource-filters", "not-configured")
		return nil, nil
	}

	if filters == nil {
		azurePluginLogger().V(1).Info("Fetching vm resources from cloud",
			"account", computeCfg.account, "resource-filters", "all(nil)")
	} else {
		azurePluginLogger().V(1).Info("Fetching vm resources from cloud",
			"account", computeCfg.account, "resource-filters", "configured")
	}
	var subscriptions []*string
	subscriptions = append(subscriptions, &computeCfg.credentials.SubscriptionID)

	var virtualMachines []*virtualMachineTable
	for _, filter := range filters {
		virtualMachineRows, _, err := getVirtualMachineTable(computeCfg.resourceGraphAPIClient, filter, subscriptions)
		if err != nil {
			return nil, err
		}
		virtualMachines = append(virtualMachines, virtualMachineRows...)
	}

	azurePluginLogger().V(1).Info("Vm instances from cloud",
		"account", computeCfg.account, "instances", len(virtualMachines))

	return virtualMachines, nil
}

func (computeCfg *computeServiceConfig) getComputeResourceFilters() ([]*string, bool) {
	var allFilters []*string

	computeFilters := computeCfg.computeFilters
	if len(computeFilters) == 0 {
		return nil, false
	}

	for _, filters := range computeCfg.computeFilters {
		// if any selector found with nil filter, skip all other selectors. As nil indicates all
		if len(filters) == 0 {
			var queries []*string
			subscriptionIDs := []string{computeCfg.credentials.SubscriptionID}
			tenantIDs := []string{computeCfg.credentials.TenantID}
			locations := []string{computeCfg.credentials.region}
			queryStr, err := getVMsBySubscriptionIDsAndTenantIDsAndLocationsMatchQuery(subscriptionIDs, tenantIDs, locations)
			if err != nil {
				azurePluginLogger().Error(err, "query string creation failed", "account", computeCfg.account)
				return nil, false
			}
			queries = append(queries, queryStr)
			return queries, true
		}
		allFilters = append(allFilters, filters...)
	}
	return allFilters, true
}

func (computeCfg *computeServiceConfig) DoResourceInventory() error {
	vnets, err := computeCfg.getVpcs()
	if err != nil {
		azurePluginLogger().Error(err, "failed to fetch cloud resources", "account", computeCfg.account)
		return err
	}

	virtualMachines, err := computeCfg.getVirtualMachines()
	if err != nil {
		azurePluginLogger().Error(err, "failed to fetch cloud resources", "account", computeCfg.account)
		return err
	} else {
		exists := struct{}{}
		vnetIDs := make(map[string]struct{})
		vpcPeers := computeCfg.buildMapVpcPeers(vnets)
		vmIDToInfoMap := make(map[internal.InstanceID]*virtualMachineTable)
		for _, vm := range virtualMachines {
			id := internal.InstanceID(strings.ToLower(*vm.ID))
			vmIDToInfoMap[id] = vm
			vnetIDs[*vm.VnetID] = exists
		}
		computeCfg.resourcesCache.UpdateSnapshot(&computeResourcesCacheSnapshot{vmIDToInfoMap, vnets, vnetIDs, vpcPeers})
	}
	return nil
}

func (computeCfg *computeServiceConfig) AddResourceFilters(selector *crdv1alpha1.CloudEntitySelector) error {
	subscriptionIDs := []string{computeCfg.credentials.SubscriptionID}
	tenantIDs := []string{computeCfg.credentials.TenantID}
	locations := []string{computeCfg.credentials.region}

	if filters, ok := convertSelectorToComputeQuery(selector, subscriptionIDs, tenantIDs, locations); ok {
		key := selector.GetNamespace() + "/" + selector.GetName()
		computeCfg.computeFilters[key] = filters
		computeCfg.selectors[key] = selector.DeepCopy()
	} else {
		return fmt.Errorf("error creating resource query filters")
	}
	return nil
}

func (computeCfg *computeServiceConfig) RemoveResourceFilters(selectorNamespacedName *types.NamespacedName) {
	delete(computeCfg.computeFilters, selectorNamespacedName.String())
	delete(computeCfg.selectors, selectorNamespacedName.String())
}

func (computeCfg *computeServiceConfig) GetInternalResourceObjects(namespace string,
	account *types.NamespacedName) map[string]*runtimev1alpha1.VirtualMachine {
	virtualMachines := computeCfg.getCachedVirtualMachines()
	vnets := computeCfg.getManagedVnets()
	vmObjects := map[string]*runtimev1alpha1.VirtualMachine{}

	for _, virtualMachine := range virtualMachines {
		// build runtimev1alpha1 VirtualMachine object.
		vmObject := computeInstanceToInternalVirtualMachineObject(virtualMachine, vnets, namespace, account, computeCfg.credentials.region)
		vmObjects[vmObject.Name] = vmObject
	}

	azurePluginLogger().V(1).Info("Internal resource objects", "account", computeCfg.account,
		"VirtualMachine objects", len(vmObjects))

	return vmObjects
}

func (computeCfg *computeServiceConfig) GetInventoryStats() *internal.CloudServiceStats {
	return computeCfg.inventoryStats
}

func (computeCfg *computeServiceConfig) ResetInventoryCache() {
	computeCfg.resourcesCache.UpdateSnapshot(nil)
	computeCfg.inventoryStats.ResetInventoryPollStats()
}

func (computeCfg *computeServiceConfig) UpdateServiceConfig(newConfig internal.CloudServiceInterface) error {
	newComputeServiceConfig := newConfig.(*computeServiceConfig)
	computeCfg.nwIntfAPIClient = newComputeServiceConfig.nwIntfAPIClient
	computeCfg.nsgAPIClient = newComputeServiceConfig.nsgAPIClient
	computeCfg.asgAPIClient = newComputeServiceConfig.asgAPIClient
	computeCfg.vnetAPIClient = newComputeServiceConfig.vnetAPIClient
	computeCfg.resourceGraphAPIClient = newComputeServiceConfig.resourceGraphAPIClient
	computeCfg.credentials = newComputeServiceConfig.credentials
	for _, value := range computeCfg.selectors {
		if err := computeCfg.AddResourceFilters(value); err != nil {
			return err
		}
	}
	return nil
}

// getVpcs invokes cloud API to fetch the list of vnets.
func (computeCfg *computeServiceConfig) getVpcs() ([]armnetwork.VirtualNetwork, error) {
	return computeCfg.vnetAPIClient.listAllComplete(context.Background())
}

func (computeCfg *computeServiceConfig) buildMapVpcPeers(results []armnetwork.VirtualNetwork) map[string][][]string {
	vpcPeers := make(map[string][][]string)

	for _, result := range results {
		if result.Properties == nil {
			continue
		}
		properties := result.Properties
		if len(properties.VirtualNetworkPeerings) > 0 {
			for _, peerConn := range properties.VirtualNetworkPeerings {
				var requesterID, destinationID, sourceID string
				accepterID := strings.ToLower(*result.ID)
				peerProperties := peerConn.Properties
				if peerProperties != nil && peerProperties.RemoteVirtualNetwork != nil {
					requesterID = strings.ToLower(*peerConn.Properties.RemoteVirtualNetwork.ID)
				}

				if peerProperties != nil && peerProperties.RemoteAddressSpace != nil &&
					len(peerProperties.RemoteAddressSpace.AddressPrefixes) > 0 {
					destinationID = strings.ToLower(*peerProperties.RemoteAddressSpace.AddressPrefixes[0])
				}
				if properties.AddressSpace != nil && len(properties.AddressSpace.AddressPrefixes) > 0 {
					sourceID = strings.ToLower(*properties.AddressSpace.AddressPrefixes[0])
				}

				vpcPeers[accepterID] = append(vpcPeers[accepterID], []string{requesterID, destinationID, sourceID})
			}
		}
	}
	return vpcPeers
}

// GetVpcInventory generates vpc object for the vpcs stored in snapshot(in cloud format) and return a map of vpc runtime objects.
func (computeCfg *computeServiceConfig) GetVpcInventory() map[string]*runtimev1alpha1.Vpc {
	snapshot := computeCfg.resourcesCache.GetSnapshot()
	if snapshot == nil {
		azurePluginLogger().Info("Compute service cache snapshot nil",
			"type", providerType, "account", computeCfg.account)
		return nil
	}

	vnetIDs := computeCfg.getManagedVnetIDs()

	// Convert to kubernetes object and return a map indexed using VnetID.
	vpcMap := map[string]*runtimev1alpha1.Vpc{}
	for _, vpc := range snapshot.(*computeResourcesCacheSnapshot).vnets {
		if strings.EqualFold(*vpc.Location, computeCfg.credentials.region) {
			managed := false
			if _, ok := vnetIDs[strings.ToLower(*vpc.ID)]; ok {
				managed = true
			}
			vpcObj := ComputeVpcToInternalVpcObject(&vpc, computeCfg.account.Namespace, computeCfg.account.Name,
				strings.ToLower(computeCfg.credentials.region), managed)
			vpcMap[strings.ToLower(*vpc.ID)] = vpcObj
		}
	}
	azurePluginLogger().V(1).Info("Cached vpcs", "account", computeCfg.account, "vpc objects", len(vpcMap))

	return vpcMap
}