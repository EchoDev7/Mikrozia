package lifecycle

import "fmt"

type Panel interface {
	ReloadTLS() error
	RestartXray() error
}

type Subscription interface{ ReloadTLS() error }

// ReloadCertificates acknowledges only after both listeners and Xray have completed.
func (c *Coordinator) ReloadCertificates(current func() (Panel, Subscription)) error {
	return c.Run("certificate reload", func() error {
		panel, subscription := current()
		if err := panel.ReloadTLS(); err != nil {
			return fmt.Errorf("reload panel TLS: %w", err)
		}
		if err := subscription.ReloadTLS(); err != nil {
			return fmt.Errorf("reload subscription TLS: %w", err)
		}
		if err := panel.RestartXray(); err != nil {
			return fmt.Errorf("reload Xray: %w", err)
		}
		return nil
	})
}
