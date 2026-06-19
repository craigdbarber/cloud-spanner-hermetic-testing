#!/bin/bash
set -e

gcloud spanner databases ddl update ledger-db \
    --instance=test-instance \
    --ddl-file=schema.sql \
    --project=test-project