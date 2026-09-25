#!/bin/bash
cd /Users/jga/Development/goAmiga/external-cpu/goAmiga
python3 diag/find_addintserver.py > /tmp/addintserver_output.txt 2>&1
echo "Done: $(wc -l < /tmp/addintserver_output.txt) lines"
