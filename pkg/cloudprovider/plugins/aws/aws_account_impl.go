// Copyright 2023 Antrea Authors.
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

package aws

import (
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	crdv1alpha1 "antrea.io/nephe/apis/crd/v1alpha1"
)

// AddProviderAccount adds and initializes given account of a cloud provider.
func (c *awsCloud) AddProviderAccount(client client.Client, account *crdv1alpha1.CloudProviderAccount) error {
	return c.cloudCommon.AddCloudAccount(client, account, account.Spec.AWSConfig)
}

// RemoveProviderAccount removes and cleans up any resources of given account of a cloud provider.
func (c *awsCloud) RemoveProviderAccount(namespacedName *types.NamespacedName) {
	c.cloudCommon.RemoveCloudAccount(namespacedName)
}

// AddAccountResourceSelector adds account specific resource selector.
func (c *awsCloud) AddAccountResourceSelector(accNamespacedName *types.NamespacedName, selector *crdv1alpha1.CloudEntitySelector) error {
	return c.cloudCommon.AddResourceFilters(accNamespacedName, selector)
}

// RemoveAccountResourcesSelector removes account specific resource selector.
func (c *awsCloud) RemoveAccountResourcesSelector(accNamespacedName, selectorNamespacedName *types.NamespacedName) {
	c.cloudCommon.RemoveResourceFilters(accNamespacedName, selectorNamespacedName)
}

func (c *awsCloud) GetAccountStatus(accNamespacedName *types.NamespacedName) (*crdv1alpha1.CloudProviderAccountStatus, error) {
	return c.cloudCommon.GetStatus(accNamespacedName)
}

// DoInventoryPoll calls cloud API to get cloud resources.
func (c *awsCloud) DoInventoryPoll(accountNamespacedName *types.NamespacedName) error {
	return c.cloudCommon.DoInventoryPoll(accountNamespacedName)
}

// ResetInventoryCache resets cloud snapshot and poll stats to nil.
func (c *awsCloud) ResetInventoryCache(accountNamespacedName *types.NamespacedName) error {
	return c.cloudCommon.ResetInventoryCache(accountNamespacedName)
}
