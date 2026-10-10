#!/bin/sh
# A demo: it prints what it would ask AWS rather than asking it.
echo "aws ssm describe-instance-information --instance ${DEN_EC2_INSTANCE_ID}"
echo "Online"
