#!/bin/bash
cd /Users/jga/Development/goAmiga/external-cpu/goAmiga
go test -count=1 -v -run TestBootDecision -timeout 120s -tags noebitengine ./amiga/ 2>&1
