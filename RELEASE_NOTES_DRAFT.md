# Release Notes - v2.1.0

## 🚀 New Features

### Configurable Timeouts for All CRUD Operations
- **Enhanced timeout handling** for Create, Read, Update, and Delete operations
- **Configurable timeouts** via Terraform resource configuration
- **Proper context propagation** to all AWS API calls
- **Graceful timeout handling** with meaningful error messages

**Resolves**: [EXT-1901] - Control Tower provider hanging operations

## 🐛 Bug Fixes

### Fixed Infinite Polling Operations
- **Fixed hanging operations** that could run indefinitely
- **Improved `waitForProvisioning`** function to respect context timeouts
- **Enhanced account deletion** reliability for long-running operations
- **Fixed linting error** in `findPrincipalUserId` function signature

## 📚 Documentation Updates

### Comprehensive Timeout Documentation
- **Added timeout configuration examples** in resource documentation
- **Provided recommended timeout values** for different use cases
- **Enhanced usage examples** with real-world scenarios
- **Added troubleshooting guidance** for timeout-related issues

## ⚡ Improvements

### Better Error Handling
- **Clear timeout error messages** instead of generic failures
- **Context cancellation detection** with detailed error information
- **Improved debugging** for long-running operations

### Enhanced Account Deletion
- **Multi-phase timeout handling** for complex deletion operations
- **Separate timeouts** for Service Catalog termination and account cleanup
- **Reliable account closure** with proper async operation handling

## 🔧 Usage

### Basic Configuration
```terraform
resource "controltower_aws_account" "example" {
  name                = "Example Account"
  email               = "example@company.com"
  organizational_unit = "Sandbox"

  sso {
    first_name = "John"
    last_name  = "Doe"
    email      = "john.doe@company.com"
  }

  timeouts {
    read   = "45m"
    create = "45m"
    update = "45m"
    delete = "45m"
  }
}
```

### Recommended Production Settings
```terraform
resource "controltower_aws_account" "production" {
  # ... your configuration ...

  close_account_on_delete = true

  timeouts {
    create = "45m"  # Account creation can take 30-45 minutes
    update = "45m"  # SSO updates and OU changes need time
    delete = "60m"  # Account deletion and cleanup can be slow
    read   = "30m"  # Reading operations are typically faster
  }
}
```

## 📋 Migration Guide

### For Existing Users

**No breaking changes** - existing configurations will continue to work with default 20-minute timeouts.

**Recommended updates**:
1. Add explicit `timeouts` blocks to your resources
2. Increase delete timeouts if you experience timeouts during account deletion
3. Configure `close_account_on_delete` based on your requirements

### Before (using defaults):
```terraform
resource "controltower_aws_account" "account" {
  name = "My Account"
  # ... configuration
  # Uses 20-minute defaults
}
```

### After (recommended):
```terraform
resource "controltower_aws_account" "account" {
  name = "My Account"
  # ... configuration
  
  timeouts {
    create = "45m"
    delete = "45m"
    update = "45m"
    read   = "30m"
  }
}
```

## 🔄 Upgrade Instructions

1. **Update the provider** to v2.1.0
2. **Add timeout configurations** to your Terraform files
3. **Test operations** in non-production environments first
4. **Apply changes** - no state migration required

## ⚠️ Important Notes

- **Account deletion operations** may now complete successfully where they previously timed out
- **Default timeouts remain 20 minutes** for backward compatibility
- **Configure longer timeouts** for production environments with complex setups
- **Account closure is asynchronous** - AWS may take additional time to fully process closures

## 🐞 Known Issues

- None at this time

## 👥 Contributors

- Implementation: Cloud Engineering Team
- Testing: Infrastructure Team
- Documentation: Platform Team

---

**Full Changelog**: [Link to GitHub compare view]
**Issues Resolved**: EXT-1901
**Release Date**: [Current Date]