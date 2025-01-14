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
	"net"
	"strconv"
	"strings"

	network "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/network/armnetwork"
	"github.com/Azure/go-autorest/autorest/to"
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/seancfoley/ipaddress-go/ipaddr"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	crdv1alpha1 "antrea.io/nephe/apis/crd/v1alpha1"
	"antrea.io/nephe/apis/runtime/v1alpha1"
	"antrea.io/nephe/pkg/cloudprovider/cloudresource"
	"antrea.io/nephe/pkg/cloudprovider/utils"
	"antrea.io/nephe/pkg/config"
)

var _ = Describe("Azure Cloud Security", func() {
	var (
		testAccountNamespacedName         = &types.NamespacedName{Namespace: "namespace01", Name: "account01"}
		testAccountNamespacedNameNotExist = &types.NamespacedName{Namespace: "notexist01", Name: "notexist01"}
		testAnpNamespace                  = &types.NamespacedName{Namespace: "test-anp-ns", Name: "test-anp"}
		testSubID                         = "SubID"
		credentials                       = "credentials"
		testClientID                      = "ClientID"
		testClientKey                     = "ClientKey"
		testTenantID                      = "TenantID"
		testRegion                        = "eastus"
		testRG                            = "testRG"
		nsgID                             = "nephe-ag-nsgID"
		atAsgID                           = "nephe-at-atapplicationsgID"
		atAsgName                         = "atapplicationsgID"
		agAsgID                           = "nephe-ag-agapplicationsgID"
		agAsgName                         = "agapplicationsgID"
		testPriority                      = int32(ruleStartPriority)
		testDirection                     = network.SecurityRuleDirectionInbound
		testSourcePortRange               = "*"
		testDestinationPortRange          = "*"
		testPrivateIP                     = "0.0.0.0"
		testProtocol                      = 6
		testFromPort                      = 22
		testToPort                        = 23
		testCidrStr                       = "192.168.1.1/24"

		testVnet01   = "testVnet01"
		testVnetID01 = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/virtualNetworks/%v",
			testSubID, testRG, testVnet01)
		testVnet02   = "testVnet02"
		testVnetID02 = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/virtualNetworks/%v",
			testSubID, testRG, testVnet02)
		testVnetID03 = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/virtualNetworks/%v",
			testSubID, testRG, "testVnet03")

		testVnetPeer01   = "testVnetPeer01"
		testVnetPeerID01 = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/virtualNetworks/%v",
			testSubID, testRG, testVnetPeer01)

		testATAsgID = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/applicationSecurityGroups/%v",
			testSubID, testRG, atAsgID)

		testAGAsgID = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/applicationSecurityGroups/%v",
			testSubID, testRG, agAsgID)

		testNsgID = fmt.Sprintf("/subscriptions/%v/resourceGroups/%v/providers/Microsoft.Network/applicationSecurityGroups/%v",
			testSubID, testRG, nsgID)
	)

	Context("SecurityGroup", func() {
		var (
			c          *azureCloud
			account    *crdv1alpha1.CloudProviderAccount
			selector   *crdv1alpha1.CloudEntitySelector
			secret     *corev1.Secret
			fakeClient client.WithWatch
			asglist    []network.ApplicationSecurityGroup
			nsg        network.SecurityGroup

			mockCtrl                        *gomock.Controller
			mockAzureServiceHelper          *MockazureServicesHelper
			mockazureNwIntfWrapper          *MockazureNwIntfWrapper
			mockazureNsgWrapper             *MockazureNsgWrapper
			mockazureAsgWrapper             *MockazureAsgWrapper
			mockazureVirtualNetworksWrapper *MockazureVirtualNetworksWrapper
			mockazureResourceGraph          *MockazureResourceGraphWrapper
			mockazureService                *MockazureServiceClientCreateInterface
		)

		BeforeEach(func() {
			var pollIntv uint = 1
			account = &crdv1alpha1.CloudProviderAccount{
				ObjectMeta: v1.ObjectMeta{
					Name:      testAccountNamespacedName.Name,
					Namespace: testAccountNamespacedName.Namespace,
				},
				Spec: crdv1alpha1.CloudProviderAccountSpec{
					PollIntervalInSeconds: &pollIntv,
					AzureConfig: &crdv1alpha1.CloudProviderAccountAzureConfig{
						Region: []string{testRegion},
						SecretRef: &crdv1alpha1.SecretReference{
							Name:      testAccountNamespacedName.Name,
							Namespace: testAccountNamespacedName.Namespace,
							Key:       credentials,
						},
					},
				},
			}

			credential := fmt.Sprintf(`{"subscriptionId": "%s",
				"clientId": "%s",
				"tenantId": "%s",
				"clientKey": "%s"
			}`, testSubID, testClientID, testTenantID, testClientKey)

			secret = &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      testAccountNamespacedName.Name,
					Namespace: testAccountNamespacedName.Namespace,
				},
				Data: map[string][]byte{
					"credentials": []byte(credential),
				},
			}

			selector = &crdv1alpha1.CloudEntitySelector{
				ObjectMeta: v1.ObjectMeta{
					Name:      "selector-VnetID",
					Namespace: testAccountNamespacedName.Namespace,
				},
				Spec: crdv1alpha1.CloudEntitySelectorSpec{
					AccountName:      testAccountNamespacedName.Name,
					AccountNamespace: testAccountNamespacedName.Namespace,
					VMSelector: []crdv1alpha1.VirtualMachineSelector{
						{
							VpcMatch: &crdv1alpha1.EntityMatch{
								MatchID: testVnet01,
							},
							VMMatch: []crdv1alpha1.EntityMatch{},
						},
					},
				},
			}

			mockCtrl = gomock.NewController(GinkgoT())
			mockAzureServiceHelper = NewMockazureServicesHelper(mockCtrl)

			mockazureService = NewMockazureServiceClientCreateInterface(mockCtrl)
			mockazureNwIntfWrapper = NewMockazureNwIntfWrapper(mockCtrl)
			mockazureNsgWrapper = NewMockazureNsgWrapper(mockCtrl)
			mockazureAsgWrapper = NewMockazureAsgWrapper(mockCtrl)
			mockazureVirtualNetworksWrapper = NewMockazureVirtualNetworksWrapper(mockCtrl)
			mockazureResourceGraph = NewMockazureResourceGraphWrapper(mockCtrl)

			mockAzureServiceHelper.EXPECT().newServiceSdkConfigProvider(gomock.Any()).Return(mockazureService, nil).Times(1)
			mockazureService.EXPECT().networkInterfaces(gomock.Any()).Return(mockazureNwIntfWrapper, nil).AnyTimes()
			mockazureService.EXPECT().securityGroups(gomock.Any()).Return(mockazureNsgWrapper, nil).AnyTimes()
			mockazureService.EXPECT().applicationSecurityGroups(gomock.Any()).Return(mockazureAsgWrapper, nil).AnyTimes()
			mockazureService.EXPECT().virtualNetworks(gomock.Any()).Return(mockazureVirtualNetworksWrapper, nil).AnyTimes()
			mockazureService.EXPECT().resourceGraph().Return(mockazureResourceGraph, nil)
			mockazureVirtualNetworksWrapper.EXPECT().listAllComplete(gomock.Any()).AnyTimes()
			mockazureResourceGraph.EXPECT().resources(gomock.Any(), gomock.Any()).Return(getResourceGraphResult(), nil).AnyTimes()
			atAsg := &network.ApplicationSecurityGroup{ID: &testATAsgID, Name: &atAsgID}
			agAsg := &network.ApplicationSecurityGroup{ID: &testAGAsgID, Name: &agAsgID}
			asglist = []network.ApplicationSecurityGroup{*agAsg, *atAsg}

			nsgrule := &network.SecurityRule{
				ID: &nsgID,
				Properties: &network.SecurityRulePropertiesFormat{
					SourceApplicationSecurityGroups:      []*network.ApplicationSecurityGroup{agAsg},
					DestinationApplicationSecurityGroups: []*network.ApplicationSecurityGroup{atAsg},
					Priority:                             &testPriority,
					SourcePortRange:                      &testSourcePortRange,
					DestinationPortRange:                 &testDestinationPortRange,
					Direction:                            &testDirection,
				},
			}

			nsg = network.SecurityGroup{
				Properties: &network.SecurityGroupPropertiesFormat{
					SecurityRules: []*network.SecurityRule{nsgrule},
				},
				ID:   &testNsgID,
				Name: &nsgID,
			}

			mockazureNsgWrapper.EXPECT().get(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().
				DoAndReturn(func(_ context.Context, _ string, _ string, _ string) (network.SecurityGroup, error) {
					return nsg, nil
				})
			mockazureNwIntfWrapper.EXPECT().listAllComplete(gomock.Any()).AnyTimes()

			mockazureAsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(*agAsg, nil).AnyTimes()
			mockazureAsgWrapper.EXPECT().get(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockazureAsgWrapper.EXPECT().delete(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
			mockazureAsgWrapper.EXPECT().listComplete(gomock.Any(), gomock.Any()).AnyTimes().
				DoAndReturn(func(_ context.Context, _ string) ([]network.ApplicationSecurityGroup, error) {
					return asglist, nil
				})

			fakeClient = fake.NewClientBuilder().Build()
			c = newAzureCloud(mockAzureServiceHelper)

			vmSelector := []crdv1alpha1.VirtualMachineSelector{
				{
					VpcMatch: &crdv1alpha1.EntityMatch{MatchID: testVnetID01},
					VMMatch:  []crdv1alpha1.EntityMatch{},
				},
			}

			err := fakeClient.Create(context.Background(), secret)
			Expect(err).Should(BeNil())
			err = c.AddProviderAccount(fakeClient, account)
			Expect(err).Should(BeNil())
			selector.Spec.VMSelector = vmSelector
			err = c.AddAccountResourceSelector(testAccountNamespacedName, selector)
			Expect(err).Should(BeNil())

			accCfg, _ := c.cloudCommon.GetCloudAccountByName(testAccountNamespacedName)
			serviceConfig := accCfg.GetServiceConfig()
			selectorNamespacedName := types.NamespacedName{Namespace: selector.Namespace, Name: selector.Name}
			inventory := serviceConfig.(*computeServiceConfig).GetCloudInventory()
			Expect(len(inventory.VmMap[selectorNamespacedName])).To(Equal(0))
			Expect(len(inventory.VpcMap)).To(Equal(0))

			vnetIDs := make(map[string]struct{})
			vnetIDs[strings.ToLower(testVnetID01)] = struct{}{}
			vnetIDs[strings.ToLower(testVnetID02)] = struct{}{}
			vnetIDs[strings.ToLower(testVnetPeerID01)] = struct{}{}
			vpcPeers := serviceConfig.(*computeServiceConfig).buildMapVpcPeers(nil)
			vpcPeers[testVnetPeerID01] = [][]string{
				{strings.ToLower(testVnetPeerID01), "destinationID", "sourceID"},
			}
			vmInfo := make([]*virtualMachineTable, 0)

			vmInfo = append(vmInfo, &virtualMachineTable{
				VnetID: &testVnetPeerID01,
				NetworkInterfaces: []*networkInterface{
					{
						PrivateIps: []*string{
							&testPrivateIP,
						},
					},
				},
			})

			cloudresource.SetCloudResourcePrefix(config.DefaultCloudResourcePrefix)

			var vnetList []network.VirtualNetwork
			vnet := new(network.VirtualNetwork)
			vnet.Name = &testVnet01
			vnet.ID = &testVnetID01
			vnetList = append(vnetList, *vnet)
			vmSnapshot := make(map[types.NamespacedName][]*virtualMachineTable)
			serviceConfig.(*computeServiceConfig).resourcesCache.UpdateSnapshot(&computeResourcesCacheSnapshot{
				vmSnapshot, vnetList, vnetIDs, vpcPeers})
			snapshot := serviceConfig.(*computeServiceConfig).resourcesCache.GetSnapshot()
			vmSnapshot[selectorNamespacedName] = vmInfo
			serviceConfig.(*computeServiceConfig).resourcesCache.UpdateSnapshot(
				&computeResourcesCacheSnapshot{vmSnapshot, snapshot.(*computeResourcesCacheSnapshot).vnets,
					snapshot.(*computeResourcesCacheSnapshot).managedVnetIDs, snapshot.(*computeResourcesCacheSnapshot).vnetPeers})
		})

		AfterEach(func() {
			mockCtrl.Finish()
		})

		Context("CreateSecurityGroup", func() {
			It("Should create security group(ASG and NSG) successfully and return ID", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				cloudSgID01, err := c.CreateSecurityGroup(webAddressGroupIdentifier01, false)
				Expect(err).Should(BeNil())
				Expect(cloudSgID01).Should(Not(BeNil()))

				webAddressGroupIdentifier02 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID02,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				cloudSgID02, err := c.CreateSecurityGroup(webAddressGroupIdentifier02, true)
				Expect(err).Should(BeNil())
				Expect(cloudSgID02).Should(Not(BeNil()))

			})

			It("Should fail to create security group", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID03,
					},
					AccountID:     testAccountNamespacedNameNotExist.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				_, err := c.CreateSecurityGroup(webAddressGroupIdentifier01, false)
				Expect(err).Should(Not(BeNil()))

			})
		})

		Context("UpdateSecurityGroup", func() {
			It("Should Update security group members", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				webAddressGroupIdentifier02 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID02,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				members := []*cloudresource.CloudResource{
					webAddressGroupIdentifier02,
				}

				err := c.UpdateSecurityGroupMembers(webAddressGroupIdentifier01, members, false)
				Expect(err).Should(BeNil())
			})

			It("Should fail to update security group members", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID03,
					},
					AccountID:     testAccountNamespacedNameNotExist.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				webAddressGroupIdentifier02 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID02,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				members := []*cloudresource.CloudResource{
					webAddressGroupIdentifier02,
				}

				err := c.UpdateSecurityGroupMembers(webAddressGroupIdentifier01, members, false)
				Expect(err).Should(Not(BeNil()))
			})
		})

		Context("UpdateSecurityRules", func() {
			It("Should update Security rules successfully", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}
				toSg := webAddressGroupIdentifier03.CloudResourceID
				toSg.Name = agAsgName

				fromSrcIP := getFromSrcIP(testCidrStr)

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							Protocol:  &testProtocol,
							FromPort:  &testFromPort,
							FromSrcIP: fromSrcIP,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							Protocol:         &testProtocol,
							ToPort:           &testToPort,
							ToDstIP:          fromSrcIP,
							ToSecurityGroups: []*cloudresource.CloudResourceID{&toSg},
						}, NpNamespacedName: testAnpNamespace.String(),
					},
				}

				mockazureNsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nsg, nil).Times(1)
				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).Should(BeNil())
			})

			It("Should update IPv6 Security rules successfully", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}
				toSg := webAddressGroupIdentifier03.CloudResourceID
				toSg.Name = agAsgName

				addRules := []*cloudresource.CloudRule{{
					Rule: &cloudresource.IngressRule{
						Protocol: &testProtocol,
						FromPort: &testFromPort,
						FromSrcIP: []*net.IPNet{{
							IP:   net.ParseIP("2600:1f16:c77:a001:fb97:21b2:a8dc:dc60"),
							Mask: net.CIDRMask(128, 128)},
						}},
					NpNamespacedName: testAnpNamespace.String()},
				}

				mockazureNsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nsg, nil).Times(1)
				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).Should(BeNil())
			})

			It("Should remove duplicate ingress security rules and update successfully", func() {
				access := network.SecurityRuleAccessAllow
				protocol := network.SecurityRuleProtocolTCP
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}
				toSg1 := cloudresource.CloudResourceID{
					Name: agAsgName + "1",
					Vpc:  testVnetID01,
				}
				toSg2 := cloudresource.CloudResourceID{
					Name: agAsgName + "2",
					Vpc:  testVnetID01,
				}

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							FromPort: &testFromPort,
							FromSrcIP: []*net.IPNet{{
								IP:   net.ParseIP("2600:1f16:c77:a001:fb97:21b2:a8dc:dc60"),
								Mask: net.CIDRMask(128, 128)},
							},
							Protocol: &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.IngressRule{
							FromPort: &testFromPort,
							FromSrcIP: []*net.IPNet{{
								IP:   net.ParseIP("2600:1f16:c77:a001:fb97:21b2:a8dc:dc61"),
								Mask: net.CIDRMask(128, 128)},
							},
							Protocol: &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.IngressRule{
							FromPort:           &testFromPort,
							FromSecurityGroups: []*cloudresource.CloudResourceID{&toSg1},
							Protocol:           &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.IngressRule{
							FromPort:           &testFromPort,
							FromSecurityGroups: []*cloudresource.CloudResourceID{&toSg2},
							Protocol:           &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					},
				}
				desc, _ := utils.GenerateCloudDescription(testAnpNamespace.String())
				nsgrules := []*network.SecurityRule{
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                               &access,
							Protocol:                             &protocol,
							DestinationApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							SourceAddressPrefixes:                []*string{to.StringPtr("2600:1f16:c77:a001:fb97:21b2:a8dc:dc60/128")},
							Priority:                             &testPriority,
							SourcePortRange:                      &testSourcePortRange,
							DestinationPortRange:                 to.StringPtr(strconv.Itoa(testFromPort)),
							Direction:                            &testDirection,
							Description:                          &desc,
						},
					},
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                               &access,
							Protocol:                             &protocol,
							DestinationApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							SourceAddressPrefixes:                []*string{to.StringPtr("2600:1f16:c77:a001:fb97:21b2:a8dc:dc61/128")},
							Priority:                             to.Int32Ptr(testPriority + 1),
							SourcePortRange:                      &testSourcePortRange,
							DestinationPortRange:                 to.StringPtr(strconv.Itoa(testFromPort)),
							Direction:                            &testDirection,
							Description:                          &desc,
						},
					},
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                               &access,
							Protocol:                             &protocol,
							SourceApplicationSecurityGroups:      []*network.ApplicationSecurityGroup{{ID: to.StringPtr(testAGAsgID + "1")}},
							DestinationApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							Priority:                             to.Int32Ptr(testPriority + 2),
							SourcePortRange:                      &testSourcePortRange,
							DestinationPortRange:                 to.StringPtr(strconv.Itoa(testFromPort)),
							Direction:                            &testDirection,
							Description:                          &desc,
						},
					},
				}

				nsg = network.SecurityGroup{
					Properties: &network.SecurityGroupPropertiesFormat{
						SecurityRules: nsgrules,
					},
					ID:   &testNsgID,
					Name: &nsgID,
				}
				asglist = []network.ApplicationSecurityGroup{
					{ID: to.StringPtr(testAGAsgID + "1"), Name: to.StringPtr(agAsgID + "1")},
					{ID: to.StringPtr(testAGAsgID + "2"), Name: to.StringPtr(agAsgID + "2")},
					{ID: to.StringPtr(testATAsgID), Name: to.StringPtr(atAsgID)},
				}
				mockazureNsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).
					Do(func(_ context.Context, _, _ string, parameters network.SecurityGroup) {
						// 4 rules and 2 deny rule.
						Expect(len(parameters.Properties.SecurityRules)).To(Equal(6))
					})

				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).Should(BeNil())
			})

			It("Should remove duplicate egress security rules and update successfully", func() {
				access := network.SecurityRuleAccessAllow
				protocol := network.SecurityRuleProtocolTCP
				outbound := network.SecurityRuleDirectionOutbound
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}
				toSg1 := cloudresource.CloudResourceID{
					Name: agAsgName + "1",
					Vpc:  testVnetID01,
				}
				toSg2 := cloudresource.CloudResourceID{
					Name: agAsgName + "2",
					Vpc:  testVnetID01,
				}

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.EgressRule{
							ToPort: &testToPort,
							ToDstIP: []*net.IPNet{{
								IP:   net.ParseIP("2600:1f16:c77:a001:fb97:21b2:a8dc:dc60"),
								Mask: net.CIDRMask(128, 128)},
							},
							Protocol: &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							ToPort: &testToPort,
							ToDstIP: []*net.IPNet{{
								IP:   net.ParseIP("2600:1f16:c77:a001:fb97:21b2:a8dc:dc61"),
								Mask: net.CIDRMask(128, 128)},
							},
							Protocol: &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							ToPort:           &testToPort,
							ToSecurityGroups: []*cloudresource.CloudResourceID{&toSg1},
							Protocol:         &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							ToPort:           &testToPort,
							ToSecurityGroups: []*cloudresource.CloudResourceID{&toSg2},
							Protocol:         &testProtocol,
						}, NpNamespacedName: testAnpNamespace.String(),
					},
				}
				desc, _ := utils.GenerateCloudDescription(testAnpNamespace.String())
				nsgrules := []*network.SecurityRule{
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                          &access,
							Protocol:                        &protocol,
							SourceApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							DestinationAddressPrefixes:      []*string{to.StringPtr("2600:1f16:c77:a001:fb97:21b2:a8dc:dc60/128")},
							Priority:                        &testPriority,
							DestinationPortRange:            to.StringPtr(strconv.Itoa(testToPort)),
							SourcePortRange:                 &testSourcePortRange,
							Direction:                       &outbound,
							Description:                     &desc,
						},
					},
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                          &access,
							Protocol:                        &protocol,
							SourceApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							DestinationAddressPrefixes:      []*string{to.StringPtr("2600:1f16:c77:a001:fb97:21b2:a8dc:dc61/128")},
							Priority:                        to.Int32Ptr(testPriority + 1),
							DestinationPortRange:            to.StringPtr(strconv.Itoa(testToPort)),
							SourcePortRange:                 &testSourcePortRange,
							Direction:                       &outbound,
							Description:                     &desc,
						},
					},
					{
						ID: &nsgID,
						Properties: &network.SecurityRulePropertiesFormat{
							Access:                               &access,
							Protocol:                             &protocol,
							DestinationApplicationSecurityGroups: []*network.ApplicationSecurityGroup{{ID: to.StringPtr(testAGAsgID + "1")}},
							SourceApplicationSecurityGroups:      []*network.ApplicationSecurityGroup{{ID: &testATAsgID}},
							Priority:                             to.Int32Ptr(testPriority + 2),
							DestinationPortRange:                 to.StringPtr(strconv.Itoa(testToPort)),
							SourcePortRange:                      &testSourcePortRange,
							Direction:                            &outbound,
							Description:                          &desc,
						},
					},
				}

				nsg = network.SecurityGroup{
					Properties: &network.SecurityGroupPropertiesFormat{
						SecurityRules: nsgrules,
					},
					ID:   &testNsgID,
					Name: &nsgID,
				}
				asglist = []network.ApplicationSecurityGroup{
					{ID: to.StringPtr(testAGAsgID + "1"), Name: to.StringPtr(agAsgID + "1")},
					{ID: to.StringPtr(testAGAsgID + "2"), Name: to.StringPtr(agAsgID + "2")},
					{ID: to.StringPtr(testATAsgID), Name: to.StringPtr(atAsgID)},
				}
				mockazureNsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(1).
					Do(func(_ context.Context, _, _ string, parameters network.SecurityGroup) {
						// 4 rules and 2 deny rule.
						Expect(len(parameters.Properties.SecurityRules)).To(Equal(6))
					})

				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).Should(BeNil())
			})

			//  Creating cloud security rules without a description field is not allowed.
			It("Should fail to update Security rules -- invalid namespacedname", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				fromSrcIP := getFromSrcIP(testCidrStr)

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							Protocol:  &testProtocol,
							FromPort:  &testFromPort,
							FromSrcIP: fromSrcIP,
						},
					}, {
						Rule: &cloudresource.EgressRule{
							Protocol: &testProtocol,
							ToPort:   &testToPort,
							ToDstIP:  fromSrcIP,
							ToSecurityGroups: []*cloudresource.CloudResourceID{
								&webAddressGroupIdentifier03.CloudResourceID,
							},
						},
					},
				}

				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).ShouldNot(BeNil())
			})

			It("Should fail to update Security rules -- asg not found", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: nsgID,
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				fromSrcIP := getFromSrcIP(testCidrStr)

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							Protocol:  &testProtocol,
							FromPort:  &testFromPort,
							FromSrcIP: fromSrcIP,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							Protocol: &testProtocol,
							ToPort:   &testToPort,
							ToDstIP:  fromSrcIP,
							ToSecurityGroups: []*cloudresource.CloudResourceID{
								&webAddressGroupIdentifier03.CloudResourceID,
							},
						}, NpNamespacedName: testAnpNamespace.String(),
					},
				}

				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).ShouldNot(BeNil())
			})

			It("Should update Security rules for Peerings", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetPeerID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}
				toSg := webAddressGroupIdentifier03.CloudResourceID
				toSg.Name = agAsgName

				cidr := ipaddr.NewIPAddressString(testCidrStr)
				subnet, _ := cidr.GetAddress().ToPrefixBlock(), cidr.GetHostAddress()
				var ipNet = net.IPNet{
					IP:   subnet.GetNetIP(),
					Mask: subnet.GetNetworkMask().Bytes(),
				}
				fromSrcIP := []*net.IPNet{
					&ipNet,
				}

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							Protocol:  &testProtocol,
							FromPort:  &testFromPort,
							FromSrcIP: fromSrcIP,
						}, NpNamespacedName: testAnpNamespace.String(),
					}, {
						Rule: &cloudresource.EgressRule{
							Protocol:         &testProtocol,
							ToPort:           &testToPort,
							ToDstIP:          fromSrcIP,
							ToSecurityGroups: []*cloudresource.CloudResourceID{&toSg},
						}, NpNamespacedName: testAnpNamespace.String(),
					},
				}

				mockazureNsgWrapper.EXPECT().createOrUpdate(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(nsg, nil).Times(1)
				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).Should(BeNil())
			})

			//  Creating cloud security rules without a description field is not allowed.
			It("Should fail to update Security rules for Peerings -- invalid namespacedname", func() {
				webAddressGroupIdentifier03 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: atAsgName,
						Vpc:  testVnetPeerID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				cidr := ipaddr.NewIPAddressString(testCidrStr)
				subnet, _ := cidr.GetAddress().ToPrefixBlock(), cidr.GetHostAddress()
				var ipNet = net.IPNet{
					IP:   subnet.GetNetIP(),
					Mask: subnet.GetNetworkMask().Bytes(),
				}
				fromSrcIP := []*net.IPNet{
					&ipNet,
				}

				addRules := []*cloudresource.CloudRule{
					{
						Rule: &cloudresource.IngressRule{
							Protocol:  &testProtocol,
							FromPort:  &testFromPort,
							FromSrcIP: fromSrcIP,
						},
					}, {
						Rule: &cloudresource.EgressRule{
							Protocol: &testProtocol,
							ToPort:   &testToPort,
							ToDstIP:  fromSrcIP,
							ToSecurityGroups: []*cloudresource.CloudResourceID{
								&webAddressGroupIdentifier03.CloudResourceID,
							},
						},
					},
				}

				err := c.UpdateSecurityGroupRules(webAddressGroupIdentifier03, addRules, []*cloudresource.CloudRule{})
				Expect(err).ShouldNot(BeNil())
			})
		})

		Context("Update VM snapshot", func() {
			It("Should update virtual machine snapshot successfully", func() {
				vmID := "testvmID"
				vmName := "testvmName"
				tag := "testtag"
				tags := make(map[string]*string)
				tags["tagtest"] = &tag
				nItfID := "testnItfID"
				testNetworkInterface := networkInterface{
					ID: &nItfID,
				}
				vmToUpdate := make([]*virtualMachineTable, 0)
				vmToUpdate = append(vmToUpdate, &virtualMachineTable{
					ID:   &vmID,
					Name: &vmName,
					Tags: tags,
					NetworkInterfaces: []*networkInterface{
						&testNetworkInterface,
					},
					VnetID: &testVnetID03,
				})

				accCfg, _ := c.cloudCommon.GetCloudAccountByName(testAccountNamespacedName)
				serviceConfig := accCfg.GetServiceConfig()
				selectorNamespacedName := types.NamespacedName{Namespace: selector.Namespace, Name: selector.Name}
				snapshot := serviceConfig.(*computeServiceConfig).resourcesCache.GetSnapshot()
				vmSnapshot := snapshot.(*computeResourcesCacheSnapshot).vms
				vmSnapshot[selectorNamespacedName] = vmToUpdate
				serviceConfig.(*computeServiceConfig).resourcesCache.UpdateSnapshot(
					&computeResourcesCacheSnapshot{vmSnapshot, snapshot.(*computeResourcesCacheSnapshot).vnets,
						snapshot.(*computeResourcesCacheSnapshot).managedVnetIDs, snapshot.(*computeResourcesCacheSnapshot).vnetPeers})
				inventory := serviceConfig.(*computeServiceConfig).GetCloudInventory()
				Expect(len(inventory.VmMap[selectorNamespacedName])).To(Equal(1))
				Expect(len(inventory.VpcMap)).To(Equal(1))
			})
		})

		Context("DeleteSecurityGroup", func() {
			It("Should delete security group(ASG and NSG) successfully", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID01,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				webAddressGroupIdentifier02 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID02,
					},
					AccountID:     testAccountNamespacedName.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				err := c.DeleteSecurityGroup(webAddressGroupIdentifier01, false)
				Expect(err).Should(BeNil())

				err = c.DeleteSecurityGroup(webAddressGroupIdentifier02, true)
				Expect(err).Should(BeNil())
			})

			It("Should fail to delete security group)", func() {
				webAddressGroupIdentifier01 := &cloudresource.CloudResource{
					Type: cloudresource.CloudResourceTypeVM,
					CloudResourceID: cloudresource.CloudResourceID{
						Name: "Web",
						Vpc:  testVnetID03,
					},
					AccountID:     testAccountNamespacedNameNotExist.String(),
					CloudProvider: string(v1alpha1.AzureCloudProvider),
				}

				err := c.DeleteSecurityGroup(webAddressGroupIdentifier01, false)
				Expect(err).Should(Not(BeNil()))
			})
		})
	})
})

func getFromSrcIP(testCidrStr string) []*net.IPNet {
	cidr := ipaddr.NewIPAddressString(testCidrStr)
	subnet, _ := cidr.GetAddress().ToPrefixBlock(), cidr.GetHostAddress()
	var ipNet = net.IPNet{
		IP:   subnet.GetNetIP(),
		Mask: subnet.GetNetworkMask().Bytes(),
	}
	fromSrcIP := []*net.IPNet{
		&ipNet,
	}
	return fromSrcIP
}
