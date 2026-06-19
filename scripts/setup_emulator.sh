#!/bin/bash
set -e

export GCLOUD_PROFILE="emulator"
#gcloud config configurations activate $GCLOUD_PROFILE
gcloud config set auth/disable_credentials true
gcloud config set project test-project
gcloud config set api_endpoint_overrides/spanner "http://localhost:9020/"

gcloud spanner instances create test-instance --config=emulator-config \
    --description="Local Dev" --nodes=1

gcloud spanner databases create ledger-db --instance=test-instance