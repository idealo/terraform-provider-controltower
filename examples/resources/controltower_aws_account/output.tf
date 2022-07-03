output "account_id" {
  description = "aws-account id"
  value       = controltower_aws_account.account.account_id
}

output "account_name" {
  description = "aws-account name"
  value       = controltower_aws_account.account.name
}
