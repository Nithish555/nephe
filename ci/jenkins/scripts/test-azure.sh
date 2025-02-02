#!/usr/bin/env bash

# Copyright 2022 Antrea Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

set -e

function echoerr {
    >&2 echo "$@"
}

_usage="Usage: $0 [arguments]

Setup and Run integration tests on Kind cluster with Azure VMs.

[arguments]
        [--azure-subscription-id <SubscriptionID>]  Azure Subscription ID.
        [--azure-app-id <AppID>]                    Azure Service Principal Application ID.
        [--azure-tenant-id <TenantID>]              Azure Service Principal Tenant ID.
        [--azure-secret <Secret>]                   Azure Service Principal Secret.
        [--azure-location <Location>]               The Azure location where the setup will be deployed. Defaults to West US 2.
        [--owner <OwnerName>]                       Setup will be prefixed with owner name.
        [--upgrade]                                 Run upgrade test."

function print_usage {
    echoerr "$_usage"
}

function print_help {
    echoerr "Try '$0 --help' for more information."
}

# Defaults
export TF_VAR_owner="ci"
export TF_VAR_location="West US 2"
export UPGRADE=false

while [[ $# -gt 0 ]]
do
key="$1"

case $key in
    --azure-subscription-id)
    export TF_VAR_azure_client_subscription_id="$2"
    shift 2
    ;;
    --azure-app-id)
    export TF_VAR_azure_client_id="$2"
    shift 2
    ;;
    --azure-tenant-id)
    export TF_VAR_azure_client_tenant_id="$2"
    shift 2
    ;;
     --azure-secret)
    export TF_VAR_azure_client_secret="$2"
    shift 2
    ;;
    --azure-location)
    export TF_VAR_location="$2"
    shift 2
    ;;
    --owner)
    export TF_VAR_owner="$2"
    shift 2
    ;;
    --upgrade)
    export UPGRADE=true
    shift 1
    ;;
    -h|--help)
    print_usage
    exit 0
    ;;
    *)    # unknown option
    echoerr "Unknown option $1"
    print_help
    exit 1
    ;;
esac
done

if [ -z "$TF_VAR_azure_client_subscription_id" ] || [ -z "$TF_VAR_azure_client_id" ] || [ -z "$TF_VAR_azure_client_tenant_id" ] || [ -z "$TF_VAR_azure_client_secret" ]; then
    echo "Azure credentials must be set."
    print_usage
    exit 1
fi

source $(dirname "${BASH_SOURCE[0]}")/common.sh
install_common_packages

echo "Building Nephe Docker image"
make build

install_kind
pull_docker_images

echo "Creating Kind cluster"
hack/install-cloud-tools.sh
ci/kind/kind-setup.sh create kind

wait_for_cert_manager "$HOME"/.kube/config

mkdir -p "$HOME"/logs
if [ "$UPGRADE" = true ] ; then
    ci/bin/upgrade.test -ginkgo.v -ginkgo.timeout 90m -ginkgo.focus=".*test-azure.*" -kubeconfig="$HOME"/.kube/config \
    -from-version=0.5.0 -to-version="latest" -chart-dir="build/charts/nephe" -cloud-provider=Azure -support-bundle-dir="$HOME"/logs
else
    # Pre install Nephe.
    helm repo add antrea https://charts.antrea.io
    helm repo update
    helm install nephe build/charts/nephe --create-namespace -n nephe-system --set cloudSyncInterval=3600

    ci/bin/integration.test -ginkgo.v -ginkgo.timeout 90m -ginkgo.focus=".*test-azure.*" -kubeconfig="$HOME"/.kube/config \
    -cloud-provider=Azure -support-bundle-dir="$HOME"/logs -pre-installed
fi
