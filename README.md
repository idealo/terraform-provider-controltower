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
$ mkdir -p ~/.terraform.d/plugins/registry.terraform.io/idealo/controltower/<some version>/darwin_arm64 # arch can be different depending on your system
$ mv bin/terraform-provider-controltower_<some version> ~/.terraform.d/plugins/registry.terraform.io/idealo/controltower/<some version>/darwin_arm64 # some version should be the future version of the provider after the changes.
```

Then you can test your changes in your terraform configuration by running `terraform init` in the directory where your terraform configuration is located. 

Make sure to define the new version under the `required_providers` block. 