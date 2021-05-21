resource "controltower_aws_account" "account" {
  name                = "Example Account"
  email               = "aws-admin@example.com"
  organizational_unit = "Sandbox"

  sso {
    first_name = "John"
    last_name  = "Doe"
    email      = "john.doe@example.com"
  }

  lifecycle {
    prevent_destroy = true
  }
}