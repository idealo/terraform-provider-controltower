package provider

import (
	"context"
	"github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/hashicorp/terraform-plugin-sdk/v2/diag"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func init() {
	// Set descriptions to support markdown syntax, this will be used in document generation
	// and the language server.
	schema.DescriptionKind = schema.StringMarkdown
}

func New(version string) func() *schema.Provider {
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
				"provider_version": {
					Description: "The version of the provider, just used for logging.",
					Type:        schema.TypeString,
					Optional:    true,
					Default:     version,
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
	accessKey := d.Get("access_key").(string)
	secretKey := d.Get("secret_key").(string)
	profile := d.Get("profile").(string)
	token := d.Get("token").(string)
	sharedCredsFile := d.Get("shared_credentials_file").(string)

	// Build configuration options for the provider
	options := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithRetryMaxAttempts(maxRetryAttempts),
	}

	// Add static credentials if access_key, secret_key, and token are provided
	if accessKey != "" && secretKey != "" {
		options = append(options, config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKey, secretKey, token),
		))
	}

	// Add shared credentials file if specified
	if sharedCredsFile != "" {
		options = append(options, config.WithSharedCredentialsFiles([]string{sharedCredsFile}))
	}

	// Add profile if specified
	if profile != "" {
		options = append(options, config.WithSharedConfigProfile(profile))
	}

	// Load the default AWS config
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	config.WithAPIOptions([]func(*smithymw.Stack) error{
		middleware.AddUserAgentKeyValue("terraform-provider-controltower", d.Get("provider_version").(string)),
	})
	if err != nil {
		return nil, diag.FromErr(err)
	}

	// Return the configured AWS SDK config
	return cfg, nil
}
