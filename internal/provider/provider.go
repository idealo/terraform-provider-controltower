package provider

import (
	"context"
	"log"

	"github.com/aws/aws-sdk-go/service/organizations"
	"github.com/aws/aws-sdk-go/service/servicecatalog"
	awsbase "github.com/hashicorp/aws-sdk-go-base"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/logging"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
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
					Description: "This is the AWS access key. It must be provided, but it can also be sourced from the `AWS_ACCESS_KEY_ID` environment variable, or via a shared credentials file if `profile` is specified.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},

				"secret_key": {
					Description: "This is the AWS secret key. It must be provided, but it can also be sourced from the `AWS_SECRET_ACCESS_KEY` environment variable, or via a shared credentials file if `profile` is specified.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},

				"profile": {
					Description: "This is the AWS profile name as set in the shared credentials file.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},

				"shared_credentials_file": {
					Description: "This is the path to the shared credentials file. If this is not set and a profile is specified, `~/.aws/credentials` will be used.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},

				"token": {
					Description: "Session token for validating temporary credentials. Typically provided after successful identity federation or Multi-Factor Authentication (MFA) login. With MFA login, this is the session token provided afterward, not the 6 digit MFA code used to get temporary credentials. It can also be sourced from the `AWS_SESSION_TOKEN` environment variable.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},
				"assume_role": assumeRoleSchema(),
				"region": {
					Description: "This is the AWS region. It must be provided, but it can also be sourced from the `AWS_DEFAULT_REGION` environment variables, or via a shared credentials file if `profile` is specified.",
					Type:        schema.TypeString,
					Required:    true,
					DefaultFunc: schema.MultiEnvDefaultFunc([]string{
						"AWS_REGION",
						"AWS_DEFAULT_REGION",
					}, nil),
					InputDefault: "us-east-1",
				},

				"max_retries": {
					Description: "This is the maximum number of times an API call is retried, in the case where requests are being throttled or experiencing transient failures. The delay between the subsequent API calls increases exponentially. If omitted, the default value is `25`.",
					Type:        schema.TypeInt,
					Optional:    true,
					Default:     25,
				},

				"skip_credentials_validation": {
					Description: "Skip the credentials validation via the STS API. Useful for AWS API implementations that do not have STS available or implemented.",
					Type:        schema.TypeBool,
					Optional:    true,
					Default:     false,
				},

				"skip_requesting_account_id": {
					Description: "Skip requesting the account ID. Useful for AWS API implementations that do not have the IAM, STS API, or metadata API.",
					Type:        schema.TypeBool,
					Optional:    true,
					Default:     false,
				},

				"skip_metadata_api_check": {
					Description: "Skip the AWS Metadata API check. Useful for AWS API implementations that do not have a metadata API endpoint. Setting to `true` prevents Terraform from authenticating via the Metadata API. You may need to use other authentication methods like static credentials, configuration variables, or environment variables.",
					Type:        schema.TypeBool,
					Optional:    true,
					Default:     false,
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
	accountid         string
	organizationsconn *organizations.Organizations
	scconn            *servicecatalog.ServiceCatalog
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

		if l, ok := d.Get("assume_role").([]interface{}); ok && len(l) > 0 && l[0] != nil {
			m := l[0].(map[string]interface{})

			if v, ok := m["duration_seconds"].(int); ok && v != 0 {
				config.AssumeRoleDurationSeconds = v
			}

			if v, ok := m["external_id"].(string); ok && v != "" {
				config.AssumeRoleExternalID = v
			}

			if v, ok := m["policy"].(string); ok && v != "" {
				config.AssumeRolePolicy = v
			}

			if policyARNSet, ok := m["policy_arns"].(*schema.Set); ok && policyARNSet.Len() > 0 {
				for _, policyARNRaw := range policyARNSet.List() {
					policyARN, ok := policyARNRaw.(string)

					if !ok {
						continue
					}

					config.AssumeRolePolicyARNs = append(config.AssumeRolePolicyARNs, policyARN)
				}
			}

			if v, ok := m["role_arn"].(string); ok && v != "" {
				config.AssumeRoleARN = v
			}

			if v, ok := m["session_name"].(string); ok && v != "" {
				config.AssumeRoleSessionName = v
			}

			if tagMapRaw, ok := m["tags"].(map[string]interface{}); ok && len(tagMapRaw) > 0 {
				config.AssumeRoleTags = make(map[string]string)

				for k, vRaw := range tagMapRaw {
					v, ok := vRaw.(string)

					if !ok {
						continue
					}

					config.AssumeRoleTags[k] = v
				}
			}

			if transitiveTagKeySet, ok := m["transitive_tag_keys"].(*schema.Set); ok && transitiveTagKeySet.Len() > 0 {
				for _, transitiveTagKeyRaw := range transitiveTagKeySet.List() {
					transitiveTagKey, ok := transitiveTagKeyRaw.(string)

					if !ok {
						continue
					}

					config.AssumeRoleTransitiveTagKeys = append(config.AssumeRoleTransitiveTagKeys, transitiveTagKey)
				}
			}

			log.Printf("[INFO] assume_role configuration set: (ARN: %q, SessionID: %q, ExternalID: %q)", config.AssumeRoleARN, config.AssumeRoleSessionName, config.AssumeRoleExternalID)
		}

		sess, accountID, _, err := awsbase.GetSessionWithAccountIDAndPartition(config)

		if err != nil {
			return nil, diag.Errorf("error configuring Terraform ControlTower Provider: %v", err)
		}

		if accountID == "" {
			diags = append(diags, diag.Diagnostic{
				Severity: diag.Warning,
				Summary:  "AWS account ID not found for provider.",
			})
		}

		client := &AWSClient{
			accountid:         accountID,
			organizationsconn: organizations.New(sess.Copy()),
			scconn:            servicecatalog.New(sess.Copy()),
		}

		return client, diags
	}
}

func assumeRoleSchema() *schema.Schema {
	return &schema.Schema{
		Type:        schema.TypeList,
		Optional:    true,
		MaxItems:    1,
		Description: "Settings for making use of the AWS Assume Role functionality.",
		Elem: &schema.Resource{
			Schema: map[string]*schema.Schema{
				"duration_seconds": {
					Type:        schema.TypeInt,
					Optional:    true,
					Description: "Seconds to restrict the assume role session duration.",
				},
				"external_id": {
					Type:        schema.TypeString,
					Optional:    true,
					Description: "Unique identifier that might be required for assuming a role in another account.",
				},
				"policy": {
					Type:         schema.TypeString,
					Optional:     true,
					Description:  "IAM Policy JSON describing further restricting permissions for the IAM Role being assumed.",
					ValidateFunc: validation.StringIsJSON,
				},
				"policy_arns": {
					Type:        schema.TypeSet,
					Optional:    true,
					Description: "Amazon Resource Names (ARNs) of IAM Policies describing further restricting permissions for the IAM Role being assumed.",
					Elem: &schema.Schema{
						Type:         schema.TypeString,
						ValidateFunc: validateArn,
					},
				},
				"role_arn": {
					Type:         schema.TypeString,
					Optional:     true,
					Description:  "Amazon Resource Name of an IAM Role to assume prior to making API calls.",
					ValidateFunc: validateArn,
				},
				"session_name": {
					Type:        schema.TypeString,
					Optional:    true,
					Description: "Identifier for the assumed role session.",
				},
				"tags": {
					Type:        schema.TypeMap,
					Optional:    true,
					Description: "Assume role session tags.",
					Elem:        &schema.Schema{Type: schema.TypeString},
				},
				"transitive_tag_keys": {
					Type:        schema.TypeSet,
					Optional:    true,
					Description: "Assume role session tag keys to pass to any subsequent sessions.",
					Elem:        &schema.Schema{Type: schema.TypeString},
				},
			},
		},
	}
}
