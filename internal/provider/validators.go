package provider

import (
	"fmt"
	"net/mail"
)

func validateEmailAddress(v interface{}, k string) (ws []string, errors []error) {
	value := v.(string)

	_, err := mail.ParseAddress(value)

	if err != nil {
		errors = append(errors, fmt.Errorf("%q must be a valid email address, parsing failed with: %value", k, err))
	}

	return ws, errors
}
