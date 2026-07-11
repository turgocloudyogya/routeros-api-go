package mikrotik

import "fmt"

type RouterOSAPIError struct {
	Message string
	ID      string
	Detail  map[string]interface{}
	Cause   error
}

func (e *RouterOSAPIError) Error() string {
	msg := fmt.Sprintf("RouterOSAPIError: %s", e.Message)
	if e.Cause != nil {
		msg += fmt.Sprintf(" (cause: %s)", e.Cause.Error())
	}
	return msg
}

type TimeoutError struct {
	RouterOSAPIError
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("RouterOSAPITimeoutError: %s", e.Message)
}

type AuthenticationError struct {
	RouterOSAPIError
}

func (e *AuthenticationError) Error() string {
	return fmt.Sprintf("RouterOSAPIAuthenticationError: %s", e.Message)
}

type ConnectionError struct {
	RouterOSAPIError
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("RouterOSAPIConnectionError: %s", e.Message)
}

type ProtocolError struct {
	RouterOSAPIError
}

func (e *ProtocolError) Error() string {
	return fmt.Sprintf("RouterOSAPIProtocolError: %s", e.Message)
}

type RetryError struct {
	RouterOSAPIError
}

func (e *RetryError) Error() string {
	return fmt.Sprintf("RouterOSAPIRetryError: %s", e.Message)
}
