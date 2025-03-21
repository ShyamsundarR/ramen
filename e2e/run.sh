#!/bin/bash

set -eu
set -o pipefail

# SPDX-FileCopyrightText: The RamenDR authors
# SPDX-License-Identifier: Apache-2.0

# This executable can be used without checking out ramen source.
go test -c -o ramen-e2e

# With an executable -test.timeout is disabled by default.
./ramen-e2e -test.v "$@"
