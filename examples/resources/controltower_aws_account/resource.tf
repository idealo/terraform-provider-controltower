resource "controltower_aws_account" "account" {
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
