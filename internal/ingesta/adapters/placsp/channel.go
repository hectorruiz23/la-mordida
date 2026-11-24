// Package placsp reads the Plataforma de Contratación del Sector Público: its
// paginated ATOM feeds of novedades and its ZIP archives of history. Page
// traversal lives beside entry translation but not inside it: a pager decides
// which documents to read, a translator turns one entry into a neutral
// publication.
package placsp

import "fmt"

// Channel is one open-data data set of PLACSP. Every channel has its own feed
// and archive naming scheme; the identity of a publication must not change
// between the feed and the archive of the same channel.
type Channel string

const (
	Profiles      Channel = "sindicacion_643"
	Aggregated    Channel = "sindicacion_1044"
	Minors        Channel = "sindicacion_1143"
	InHouse       Channel = "sindicacion_1383"
	Consultations Channel = "sindicacion_1403"
)

// Source is the provenance prefix stored with every publication. The channel
// travels in it so a version read from a feed and a version read from an
// archive share the same source identity.
func (c Channel) Source() string { return "placsp:" + string(c) }

// Tender reports whether the channel publishes tenders. Encargos a medios
// propios (1383) and consultas preliminares (1403) are different legal objects
// and must not enter the tender stream, even when their XML looks similar.
func (c Channel) Tender() bool {
	switch c {
	case Profiles, Aggregated, Minors:
		return true
	default:
		return false
	}
}

func (c Channel) validate() error {
	switch c {
	case Profiles, Aggregated, Minors, InHouse, Consultations:
		return nil
	default:
		return fmt.Errorf("placsp: unknown channel %q", string(c))
	}
}

func (c Channel) validateTender() error {
	if err := c.validate(); err != nil {
		return err
	}
	if !c.Tender() {
		return fmt.Errorf("placsp: channel %s does not publish tenders", c)
	}
	return nil
}

// baseMember is the document the catalogue says to start from: the current
// feed extract whose links lead to the rest of the package.
func (c Channel) baseMember() string {
	switch c {
	case Profiles:
		return "licitacionesPerfilesContratanteCompleto3.atom"
	case Aggregated:
		return "PlataformasAgregadasSinMenores.atom"
	case Minors:
		return "contratosMenoresPerfilesContratantes.atom"
	default:
		return ""
	}
}
