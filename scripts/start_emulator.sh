#!/bin/bash
set -e

export SPANNER_EMULATOR_HOST="localhost:9010"
docker pull gcr.io/cloud-spanner-emulator/emulator
docker run -d --name spanner-emulator -p 9010:9010 -p 9020:9020 gcr.io/cloud-spanner-emulator/emulator