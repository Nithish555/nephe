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

package common

import (
	"reflect"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "antrea.io/nephe/apis/crd/v1alpha1"
	runtimev1alpha1 "antrea.io/nephe/apis/runtime/v1alpha1"
	"antrea.io/nephe/pkg/cloudprovider/securitygroup"
)

var (
	RuntimeAPIVersion               = "runtime.cloud.antrea.io/v1alpha1"
	VirtualMachineRuntimeObjectKind = reflect.TypeOf(runtimev1alpha1.VirtualMachine{}).Name()

	MaxCloudResourceResponse int64 = 100
)

type ProviderType runtimev1alpha1.CloudProvider
type InstanceID string

// CloudInterface is an abstract providing set of methods to be implemented by cloud providers.
type CloudInterface interface {
	// ProviderType returns the cloud provider type (aws, azure, gce etc).
	ProviderType() (providerType ProviderType)

	AccountMgmtInterface

	ComputeInterface

	SecurityInterface
}

// AccountMgmtInterface is an abstract providing set of methods to manage cloud account details to be implemented by cloud providers.
type AccountMgmtInterface interface {
	// AddProviderAccount adds and initializes given account of a cloud provider.
	AddProviderAccount(client client.Client, account *crdv1alpha1.CloudProviderAccount) error
	// RemoveProviderAccount removes and cleans up any resources of given account of a cloud provider.
	RemoveProviderAccount(namespacedName *types.NamespacedName)
	// AddAccountResourceSelector adds account specific resource selector.
	AddAccountResourceSelector(accNamespacedName *types.NamespacedName, selector *crdv1alpha1.CloudEntitySelector) error
	// RemoveAccountResourcesSelector removes account specific resource selector.
	RemoveAccountResourcesSelector(accNamespacedName, selectorNamespacedName *types.NamespacedName)
	// GetAccountStatus gets accounts status.
	GetAccountStatus(accNamespacedName *types.NamespacedName) (*crdv1alpha1.CloudProviderAccountStatus, error)
	// DoInventoryPoll calls cloud API to get cloud resources.
	DoInventoryPoll(accountNamespacedName *types.NamespacedName) error
	// ResetInventoryCache resets cloud snapshot and poll stats to nil.
	ResetInventoryCache(accountNamespacedName *types.NamespacedName) error
	// GetVpcInventory gets vpc inventory from internal stored snapshot.
	GetVpcInventory(accountNamespacedName *types.NamespacedName) (map[string]*runtimev1alpha1.Vpc, error)
}

// ComputeInterface is an abstract providing set of methods to get Instance details to be implemented by cloud providers.
type ComputeInterface interface {
	// InstancesGivenProviderAccount returns all VM instances of a given cloud provider account, as a map of
	// runtime VirtualMachine objects.
	InstancesGivenProviderAccount(namespacedName *types.NamespacedName) (map[string]*runtimev1alpha1.VirtualMachine, error)
}

type SecurityInterface interface {
	// CreateSecurityGroup creates cloud security group corresponding to provided security group, if it does not already exist.
	// If it exists, returns the existing cloud SG ID.
	CreateSecurityGroup(securityGroupIdentifier *securitygroup.CloudResource, membershipOnly bool) (*string, error)
	// UpdateSecurityGroupRules updates cloud security group corresponding to provided appliedTo group with provided rules.
	// addRules and rmRules are the changed rules, allRules are rules from all nps of the security group.
	UpdateSecurityGroupRules(appliedToGroupIdentifier *securitygroup.CloudResource, addRules, rmRules,
		allRules []*securitygroup.CloudRule) error
	// UpdateSecurityGroupMembers updates membership of cloud security group corresponding to provided security group. Only
	// provided computeResources will remain attached to cloud security group. UpdateSecurityGroupMembers will also make sure that
	// after membership update, if compute resource is no longer attached to any nephe created cloud security group, then
	// compute resource will get moved to cloud default security group.
	UpdateSecurityGroupMembers(securityGroupIdentifier *securitygroup.CloudResource, computeResourceIdentifier []*securitygroup.CloudResource,
		membershipOnly bool) error
	// DeleteSecurityGroup will delete the cloud security group corresponding to provided security group. DeleteSecurityGroup expects that
	// UpdateSecurityGroupMembers and UpdateSecurityGroupRules is called prior to calling delete. DeleteSecurityGroup as part of delete,
	// do the best effort to find resources using this security group and detach the cloud security group from those resources. Also, if the
	// compute resource is attached to only this security group, it will be moved to cloud default security group.
	DeleteSecurityGroup(securityGroupIdentifier *securitygroup.CloudResource, membershipOnly bool) error
	// GetEnforcedSecurity returns the cloud view of enforced security.
	GetEnforcedSecurity() []securitygroup.SynchronizationContent
}