# Control Tower Terraform Provider (terraform-provider-controltower)

## Documentation

You can browse documentation on the [Terraform provider registry](https://registry.terraform.io/providers/idealo/controltower/latest/docs).

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine.

To compile the provider, run `make build`. This will build the provider and put the provider binary in the `bin` directory under the project's root folder.

To generate or update documentation, run `go generate`.

In order to run the full suite of Acceptance tests, run `make testacc`.

*Note:* Acceptance tests create real resources, and often cost money to run.

```sh
$ make testacc
```

## Testing the Provider Locally

You can test the provider locally before creating a PR by following the steps below:

```sh
$ make build # make sure to have the build version in the executable name as a postfix e.g. terraform-provider-controltower_v2.0.0
```
create a `~/.terraformrc` file your home directory with the following content:
```hcl
provider_installation {
  dev_overrides {
    "registry.terraform.io/idealo/controltower" = "path-to-the-built-binary/terraform-provider-controltower"  # e.g /Users/username/repo/terraform-provider-controltower/bin/terraform-provider-controltower"
  }
  # For all other providers, install them directly from their origin provider
  # registries as normal. If you omit this, Terraform will _only_ use
  # the dev_overrides block, and so no other providers will be available.
  direct {}
}

```
Then you can test your changes in your terraform configuration by running `terraform init` (which will fail but that's expected) and then `terraform plan` in the directory where your terraform configuration is located. 

Make sure to define the new version under the `required_providers` block. 

A complete reference can be found [here](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers).