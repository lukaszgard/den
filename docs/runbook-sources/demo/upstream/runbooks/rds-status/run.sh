#!/bin/sh
# A demo: it prints what it would ask AWS rather than asking it.
echo "aws rds describe-db-clusters --profile ${AWS_PROFILE} --region ${AWS_REGION}"
echo "orders-cluster   available   16.4"
