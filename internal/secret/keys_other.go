//go:build !linux

package secret

func LoadKeys(string) (Ring, error)                 { return Ring{}, ErrUnavailable }
func InitializeKeys(string, []byte) (Ring, error)   { return Ring{}, ErrUnavailable }
func PrepareKey(string) (Ring, error)               { return Ring{}, ErrUnavailable }
func ActivateKeys(string, int) error                { return ErrUnavailable }
func ReadPrivateFile(string, int64) ([]byte, error) { return nil, ErrUnavailable }
