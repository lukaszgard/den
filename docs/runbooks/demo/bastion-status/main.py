# /// script
# requires-python = ">=3.10"
# dependencies = ["boto3"]
# ///
"""Is the environment's bastion running and reachable over SSM? A runbook in Python.

den sets AWS_PROFILE, AWS_DEFAULT_REGION and DEN_EC2_INSTANCE_ID from the runbook's
env, so the clients need no configuration: boto3 reads the first two from the environment.
Read-only: ec2:DescribeInstances and ssm:DescribeInstanceInformation.
"""

import os
import sys
from datetime import datetime, timezone

import boto3
from botocore.exceptions import BotoCoreError, ClientError


def main() -> int:
    instance_id = os.environ.get("DEN_EC2_INSTANCE_ID")
    if not instance_id:
        print("DEN_EC2_INSTANCE_ID is not set: run it with an env that has an ec2_instance_id", file=sys.stderr)
        return 2

    try:
        reservations = boto3.client("ec2").describe_instances(InstanceIds=[instance_id])["Reservations"]
        registered = boto3.client("ssm").describe_instance_information(
            Filters=[{"Key": "InstanceIds", "Values": [instance_id]}]
        )["InstanceInformationList"]
    except (BotoCoreError, ClientError) as err:
        print(f"AWS call failed: {err}", file=sys.stderr)
        return 1

    if not reservations or not reservations[0]["Instances"]:
        print(f"{instance_id}: no such instance", file=sys.stderr)
        return 1

    instance = reservations[0]["Instances"][0]
    state = instance["State"]["Name"]
    up = datetime.now(timezone.utc) - instance["LaunchTime"]
    ssm = registered[0]["PingStatus"] if registered else "not registered"

    print(f"{'instance':<10}{instance_id}  ({instance['InstanceType']}, {instance['Placement']['AvailabilityZone']})")
    print(f"{'state':<10}{state}, launched {up.days}d {up.seconds // 3600}h ago")
    print(f"{'ssm':<10}{ssm}")

    if state == "running" and ssm == "Online":
        print("bastion is reachable")
        return 0
    print("bastion is not reachable over SSM")
    return 1


if __name__ == "__main__":
    sys.exit(main())
