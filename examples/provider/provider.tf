terraform {
  required_providers {
    controltower = {
      source  = "idealo/controltower"
      version = "~> 2.0"
    }
  }
}

provider "controltower" {
  region = "eu-central-1"
}