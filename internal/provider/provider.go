package provider

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
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

func New() func() *schema.Provider {
	return func() *schema.Provider {
		p := &schema.Provider{
			Schema: map[string]*schema.Schema{

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
					Description: "Session token for validating temporary credentials. Typically provided after successful identity federation or Multi-Factor Authentication (MFA) login. With MFA login, this is the session token provided afterward, not the 6 digit MFA code used to get temporary credentials. It can also be sourced from the AWS_SESSION_TOKEN environment variable.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     "",
				},

				"max_retries": {
					Description: "This is the maximum number of times an API call is retried, in the case where requests are being throttled or experiencing transient failures. The delay between the subsequent API calls increases exponentially. If omitted, the default value is `25`.",
					Type:        schema.TypeInt,
					Optional:    true,
					Default:     25,
				},
			},
			DataSourcesMap: map[string]*schema.Resource{},
			ResourcesMap: map[string]*schema.Resource{
				"controltower_aws_account": resourceAWSAccount(),
			},
			ConfigureContextFunc: configureProvider,
		}

		return p
	}
}
func configureProvider(ctx context.Context, d *schema.ResourceData) (interface{}, diag.Diagnostics) {
	region := d.Get("region").(string)
	maxRetryAttempts := d.Get("max_retries").(int)
	sharedCredsFile := d.Get("shared_credentials_file").(string)
	profile := d.Get("profile").(string)
	accessKey := d.Get("access_key").(string)
	secretKey := d.Get("secret_key").(string)
	token := d.Get("token").(string)

	options := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithRetryMaxAttempts(maxRetryAttempts),
	}

	// Add static credentials if access_key and secret_key are provided
	if accessKey != "" && secretKey != "" {
		options = append(options, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, token),
		))
	}

	// Add shared credentials file and profile if specified
	if sharedCredsFile != "" {
		options = append(options, config.WithSharedCredentialsFiles([]string{sharedCredsFile}))
	}
	if profile != "" {
		options = append(options, config.WithSharedConfigProfile(profile))
	}

	// Load AWS configuration
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, diag.FromErr(err)
	}

	return cfg, nil
}
