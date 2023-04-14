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

package cloudprovideraccount

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	mock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	fakewatch "k8s.io/client-go/kubernetes/fake"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	antreanetworking "antrea.io/antrea/pkg/apis/controlplane/v1beta2"
	antreatypes "antrea.io/antrea/pkg/apis/crd/v1alpha2"
	"antrea.io/nephe/apis/crd/v1alpha1"
	cloud "antrea.io/nephe/apis/crd/v1alpha1"
	mockaccmanager "antrea.io/nephe/pkg/testing/accountmanager"
	"antrea.io/nephe/pkg/util"
)

var (
	mockCtrl       *mock.Controller
	mockAccManager *mockaccmanager.MockInterface
	scheme         = runtime.NewScheme()
)

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	_ = clientgoscheme.AddToScheme(scheme)
	_ = antreatypes.AddToScheme(scheme)
	_ = cloud.AddToScheme(scheme)
	_ = antreanetworking.AddToScheme(scheme)
})

func TestCloud(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CloudProviderAccount controller")
}

var _ = Describe("CloudProviderAccount Controller", func() {
	Context("CPA workflow", func() {
		var (
			credentials               = "credentials"
			testAccountNamespacedName = types.NamespacedName{Namespace: "namespace01", Name: "account01"}
			testSecretNamespacedName  = types.NamespacedName{Namespace: "namespace01", Name: "secret01"}
			account                   *v1alpha1.CloudProviderAccount
			reconciler                *CloudProviderAccountReconciler
			secret                    *corev1.Secret
			fakeClient                client.WithWatch
			pollIntv                  uint
		)

		BeforeEach(func() {
			newScheme := runtime.NewScheme()
			utilruntime.Must(clientgoscheme.AddToScheme(newScheme))
			utilruntime.Must(v1alpha1.AddToScheme(newScheme))

			fakeClient = fake.NewClientBuilder().WithScheme(newScheme).Build()
			mockCtrl = mock.NewController(GinkgoT())
			mockAccManager = mockaccmanager.NewMockInterface(mockCtrl)
			reconciler = &CloudProviderAccountReconciler{
				Log:        logf.Log,
				Client:     fakeClient,
				Scheme:     scheme,
				mutex:      sync.Mutex{},
				AccManager: mockAccManager,
			}

			pollIntv = 1
			account = &v1alpha1.CloudProviderAccount{
				ObjectMeta: v1.ObjectMeta{
					Name:      testAccountNamespacedName.Name,
					Namespace: testAccountNamespacedName.Namespace,
				},
				Spec: v1alpha1.CloudProviderAccountSpec{
					PollIntervalInSeconds: &pollIntv,
					AWSConfig: &v1alpha1.CloudProviderAccountAWSConfig{
						Region: "us-east-1",
						SecretRef: &v1alpha1.SecretReference{
							Name:      testSecretNamespacedName.Name,
							Namespace: testSecretNamespacedName.Namespace,
							Key:       credentials,
						},
					},
				},
			}

			credential := `{"accessKeyId": "keyId","accessKeySecret": "keySecret"}`
			secret = &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      testSecretNamespacedName.Name,
					Namespace: testSecretNamespacedName.Namespace,
				},
				Data: map[string][]byte{
					"credentials": []byte(credential),
				},
			}
		})
		It("Account add with unknown cloud type", func() {
			account = &v1alpha1.CloudProviderAccount{
				ObjectMeta: v1.ObjectMeta{
					Name:      testAccountNamespacedName.Name,
					Namespace: testAccountNamespacedName.Namespace,
				},
				Spec: v1alpha1.CloudProviderAccountSpec{},
			}
			err := reconciler.processCreateOrUpdate(&testAccountNamespacedName, account)
			Expect(err.Error()).Should(ContainSubstring(util.ErrorMsgUnknownCloudProvider))
		})
		It("Account add and delete workflow", func() {
			accountCloudType, err := util.GetAccountProviderType(account)
			Expect(err).ShouldNot(HaveOccurred())
			mockAccManager.EXPECT().AddAccount(&testAccountNamespacedName, accountCloudType, account).Return(nil).Times(1)
			mockAccManager.EXPECT().RemoveAccount(&testAccountNamespacedName).Return(nil).Times(1)
			err = reconciler.processCreateOrUpdate(&testAccountNamespacedName, account)
			Expect(err).ShouldNot(HaveOccurred())
			err = reconciler.processDelete(&testAccountNamespacedName)
			Expect(err).ShouldNot(HaveOccurred())
		})
		It("Secret watcher", func() {
			var err error
			reconciler.clientset = fakewatch.NewSimpleClientset()
			credential := `{"accessKeyId": "keyId","accessKeySecret": "Secret"}`
			err = os.Setenv("POD_NAMESPACE", testSecretNamespacedName.Namespace)
			Expect(err).ShouldNot(HaveOccurred())
			// Create a secret using both fakeClient and clientset, since secret watcher uses cleintset
			// while reconciler using client from controller run-time. Both secret watcher and reconciler
			// should be able to retrieve the secret object.
			_, err = reconciler.clientset.CoreV1().Secrets(testSecretNamespacedName.Namespace).
				Create(context.Background(), secret, v1.CreateOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			_ = fakeClient.Create(context.Background(), secret)
			// Create CPA.
			_ = fakeClient.Create(context.Background(), account)
			go func() {
				reconciler.setupSecretWatcher()
			}()
			time.Sleep(time.Second * 10)
			// Update the valid secret credentials.
			secret = &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      testSecretNamespacedName.Name,
					Namespace: testSecretNamespacedName.Namespace,
				},
				Data: map[string][]byte{
					"credentials": []byte(credential),
				},
			}
			_, err = reconciler.clientset.CoreV1().Secrets(testSecretNamespacedName.Namespace).
				Update(context.Background(), secret, v1.UpdateOptions{})
			Expect(err).ShouldNot(HaveOccurred())
			// Update the invalid secret credentials.
			secret = &corev1.Secret{
				ObjectMeta: v1.ObjectMeta{
					Name:      testSecretNamespacedName.Name,
					Namespace: testSecretNamespacedName.Namespace,
				},
				Data: map[string][]byte{
					"credentials": []byte("credentialg"),
				},
			}
			_ = fakeClient.Update(context.Background(), secret)
			_, err = reconciler.clientset.CoreV1().Secrets(testSecretNamespacedName.Namespace).
				Update(context.Background(), secret, v1.UpdateOptions{})
			Expect(err).ShouldNot(HaveOccurred())
		})
	})
})