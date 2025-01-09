# Basic Example
resource "controltower_aws_account" "basic_account_example" {
  name                = "Example Account"
  email               = "aws-admin@example.com"
  organizational_unit = "Sandbox"

  organizational_unit_id_on_delete = "ou-some-id"

  sso {
    first_name = "John"
    last_name  = "Doe"
    email      = "john.doe@example.com"
  }
}
##########################################################################################################

# Extended Example to handle account reassignment upon update
locals {
  username = "john.doe@example.de"
}

data "aws_ssoadmin_instances" "sso" {}

# Normally AWSAdministratorAccess is the default permission set assigned to the account upon creation by Control Tower.
data "aws_ssoadmin_permission_set" "permission" {

  instance_arn = tolist(data.aws_ssoadmin_instances.sso.arns)[0]
  name         = "AWSAdministratorAccess"
}

data "aws_identitystore_user" "user" {

  identity_store_id = tolist(data.aws_ssoadmin_instances.sso.identity_store_ids)[0]
  alternate_identifier {
    unique_attribute {
      attribute_path  = "UserName"
      attribute_value = local.username
    }
  }
}

resource "controltower_aws_account" "extended_example_account" {
  name                = "Extended Example Account"
  email               = local.username
  organizational_unit = "Sandbox"

  organizational_unit_id_on_delete = "ou-some-id"

  sso {
    first_name                          = "John"
    last_name                           = "Doe"
    email                               = "aws-admin@example.com"
    instance_arn                        = tolist(data.aws_ssoadmin_instances.sso.arns)[0]
    principal_id                        = data.aws_identitystore_user.user.user_id
    remove_account_assignment_on_update = true
    permission_set_arn                  = data.aws_ssoadmin_permission_set.permission.arn

  }
}
