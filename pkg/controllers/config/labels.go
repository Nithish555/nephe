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

package config

const (
	// Well known labels on ExternalEntities so that they can be selected by Antrea NetworkPolicies.
	ExternalEntityLabelKeyTagPrefix    = "tag-"
	ExternalEntityLabelKeyNamespace    = LabelPrefixNephe + "namespace"
	ExternalEntityLabelKeyKind         = LabelPrefixNephe + "kind"
	ExternalEntityLabelKeyOwnerVm      = LabelPrefixNephe + "owner-vm"
	ExternalEntityLabelKeyOwnerVmVpc   = LabelPrefixNephe + "owner-vm-vpc"
	ExternalEntityLabelKeyCloudRegion  = LabelPrefixNephe + "cloud-region"
	ExternalEntityLabelKeyCloudVpcUID  = LabelPrefixNephe + "cloud-vpc-uid"
	ExternalEntityLabelKeyCloudVpcName = LabelPrefixNephe + "cloud-vpc-name"
	ExternalEntityLabelKeyCloudVmUID   = LabelPrefixNephe + "cloud-vm-uid"
	ExternalEntityLabelKeyCloudVmName  = LabelPrefixNephe + "cloud-vm-name"
)

const (
	// TODO: Prefix with VirtualMachine? or move to inventory package?
	LabelPrefixNephe           = "nephe.io/"
	LabelCloudAccountName      = LabelPrefixNephe + "cpa-name"
	LabelCloudAccountNamespace = LabelPrefixNephe + "cpa-namespace"
	LabelVpcName               = LabelPrefixNephe + "vpc-name"
	LabelCloudRegion           = LabelPrefixNephe + "cloud-region"
	LabelCloudVpcUID           = LabelPrefixNephe + "cloud-vpc-uid"
	LabelCloudVmUID            = LabelPrefixNephe + "cloud-vm-uid"
)
