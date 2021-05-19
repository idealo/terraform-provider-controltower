package provider

import (
	"context"

	"github.com/aws/aws-sdk-go/service/servicecatalog"
	awsbase "github.com/hashicorp/aws-sdk-go-base"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/logging"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func init() {
	// Set descriptions to support markdown syntax, this will be used in document generation
	// and the language server.
	schema.DescriptionKind = schema.StringMarkdown

	// Customize the content of descriptions when output. For example you can add defaults on
	// to the exported descriptions if present.
	// schema.SchemaDescriptionBuilder = func(s *schema.Schema) string {
	// 	desc := s.Description
	// 	if s.Default != nil {
	// 		desc += fmt.Sprintf(" Defaults to `%v`.", s.Default)
	// 	}
	// 	return strings.TrimSpace(desc)
	// }
}

func New(version string) func() *schema.Provider {
	return func() *schema.Provider {
		p := &schema.Provider{
			Schema: map[string]*schema.Schema{
				"access_key": {
					Type:     schema.TypeString,
					Optional: true,
					Default:  "",
				},

				"secret_key": {
					Type:     schema.TypeString,
					Optional: true,
					Default:  "",
				},

				"profile": {
					Type:     schema.TypeString,
					Optional: true,
					Default:  "",
				},

				"shared_credentials_file": {
					Type:     schema.TypeString,
					Optional: true,
					Default:  "",
				},

				"token": {
					Type:     schema.TypeString,
					Optional: true,
					Default:  "",
				},

				"region": {
					Type:     schema.TypeString,
					Required: true,
					DefaultFunc: schema.MultiEnvDefaultFunc([]string{
						"AWS_REGION",
						"AWS_DEFAULT_REGION",
					}, nil),
					InputDefault: "us-east-1",
				},

				"max_retries": {
					Type:     schema.TypeInt,
					Optional: true,
					Default:  25,
				},

				"skip_credentials_validation": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},

				"skip_requesting_account_id": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},

				"skip_metadata_api_check": {
					Type:     schema.TypeBool,
					Optional: true,
					Default:  false,
				},
			},
			DataSourcesMap: map[string]*schema.Resource{},
			ResourcesMap: map[string]*schema.Resource{
				"controltower_aws_account": resourceAWSAccount(),
			},
		}

		p.ConfigureContextFunc = configure(version, p)

		return p
	}
}

type AWSClient struct {
	accountid string
	scconn    *servicecatalog.ServiceCatalog
}

func configure(version string, p *schema.Provider) func(context.Context, *schema.ResourceData) (interface{}, diag.Diagnostics) {
	return func(c context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
		var diags diag.Diagnostics

		config := &awsbase.Config{
			AccessKey:               d.Get("access_key").(string),
			SecretKey:               d.Get("secret_key").(string),
			Profile:                 d.Get("profile").(string),
			Token:                   d.Get("token").(string),
			Region:                  d.Get("region").(string),
			CredsFilename:           d.Get("shared_credentials_file").(string),
			DebugLogging:            logging.IsDebugOrHigher(),
			MaxRetries:              d.Get("max_retries").(int),
			SkipCredsValidation:     d.Get("skip_credentials_validation").(bool),
			SkipRequestingAccountId: d.Get("skip_requesting_account_id").(bool),
			SkipMetadataApiCheck:    d.Get("skip_metadata_api_check").(bool),
			UserAgentProducts: []*awsbase.UserAgentProduct{
				{Name: "APN", Version: "1.0"},
				{Name: "HashiCorp", Version: "1.0"},
				{Name: "Terraform", Version: p.TerraformVersion, Extra: []string{"+https://www.terraform.io"}},
				{Name: "terraform-provider-controltower", Version: version, Extra: []string{"+https://registry.terraform.io/providers/idealo/controltower"}},
			},
		}

		sess, accountID, _, err := awsbase.GetSessionWithAccountIDAndPartition(config)
		if err != nil {
			return nil, diag.Errorf("Error configuring Terraform ControlTower Provider: %v", err)
		}

		if accountID == "" {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Warning,
				Summary:  "AWS account ID not found for provider.",
			})
		}

		client := &AWSClient{
			accountid: accountID,
			scconn:    servicecatalog.New(sess.Copy()),
		}

		return client, diags
	}
}
