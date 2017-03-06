Command ec2-reservations reports mismatch of running on-demand ec2 instances
and number of reserved instances. It does not take into account additional
instance attributes like Linux/non-linux, VPC/non-VPC, it only matches
instances/reservations based on type (like m3.medium) and availability zone
(in case of AZ-scoped reservations).

Use regular AWS SDK variables to set authentication and region:
AWS_SECRET_KEY, AWS_ACCESS_KEY, AWS_REGION.
