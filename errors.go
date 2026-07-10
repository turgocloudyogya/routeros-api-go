package mikrotik

import "fmt"

type RouterOSAPIError struct {
	Message string
	ID      string
	Detail  map[string]string
}

func (e *RouterOSAPIError) Error() string {
	return fmt.Sprintf("RouterOSAPIError: %s", e.Message)
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
