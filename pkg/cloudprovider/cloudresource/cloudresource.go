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

package cloudresource

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"reflect"
	"strings"

	runtimev1alpha1 "antrea.io/nephe/apis/runtime/v1alpha1"
)

var (
	ControllerPrefix             string
	ControllerAddressGroupPrefix string
	ControllerAppliedToPrefix    string
)

// CloudResourceType specifies the type of cloud resource.
type CloudResourceType string

var (
	CloudResourceTypeVM  = CloudResourceType(reflect.TypeOf(runtimev1alpha1.VirtualMachine{}).Name())
	CloudResourceTypeNIC = CloudResourceType(reflect.TypeOf(runtimev1alpha1.NetworkInterface{}).Name())
)

func SetCloudResourcePrefix(CloudResourcePrefix string) {
	ControllerPrefix = CloudResourcePrefix
}

func GetControllerAddressGroupPrefix() string {
	ControllerAddressGroupPrefix = ControllerPrefix + "-ag-"
	return ControllerAddressGroupPrefix
}

func GetControllerAppliedToPrefix() string {
	ControllerAppliedToPrefix = ControllerPrefix + "-at-"
	return ControllerAppliedToPrefix
}

type CloudResourceID struct {
	Name string
	Vpc  string
}

// CloudResource uniquely identify a cloud resource.
type CloudResource struct {
	Type CloudResourceType
	CloudResourceID
	// TODO: Rename AccountID to AccountNameSpacedName.
	AccountID     string
	CloudProvider string
}

func (c *CloudResource) String() string {
	return string(c.Type) + "/" + c.CloudResourceID.String()
}

func (c *CloudResourceID) GetCloudName(membershipOnly bool) string {
	if membershipOnly {
		return fmt.Sprintf("%v%v", GetControllerAddressGroupPrefix(), strings.ToLower(c.Name))
	}
	return fmt.Sprintf("%v%v", GetControllerAppliedToPrefix(), strings.ToLower(c.Name))
}

func (c *CloudResourceID) String() string {
	return c.Name + "/" + c.Vpc
}

// Used to create a rule description.
const (
	Name      = "Name"
	Namespace = "Ns"
)

type CloudRuleDescription struct {
	Name      string
	Namespace string
}

func (r *CloudRuleDescription) String() string {
	return Name + ":" + r.Name + ", " +
		Namespace + ":" + r.Namespace
}

type Rule interface {
	isRule()
}

// IngressRule specifies one ingress rule of cloud SecurityGroup.
type IngressRule struct {
	FromPort           *int
	FromSrcIP          []*net.IPNet
	FromSecurityGroups []*CloudResourceID
	Protocol           *int
	AppliedToGroup     map[string]struct{}
}

func (i *IngressRule) isRule() {}

// EgressRule specifies one egress rule of cloud SecurityGroup.
type EgressRule struct {
	ToPort           *int
	ToDstIP          []*net.IPNet
	ToSecurityGroups []*CloudResourceID
	Protocol         *int
	AppliedToGroup   map[string]struct{}
}

func (e *EgressRule) isRule() {}

type CloudRule struct {
	Hash             string `json:"-"`
	Rule             Rule
	NpNamespacedName string `json:"-"`
	AppliedToGrp     string
}

func (c *CloudRule) GetHash() string {
	hash := sha1.New()
	bytes, _ := json.Marshal(c)
	hash.Write(bytes)
	hashValue := hex.EncodeToString(hash.Sum(nil))
	return hashValue
}

// SynchronizationContent returns a SecurityGroup content in cloud.
type SynchronizationContent struct {
	Resource                   CloudResource
	MembershipOnly             bool
	Members                    []CloudResource
	MembersWithOtherSGAttached []CloudResource
	IngressRules               []CloudRule
	EgressRules                []CloudRule
}
