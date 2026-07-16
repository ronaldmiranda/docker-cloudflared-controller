package controller

import (
	"context"
	"fmt"
	"strings"

	"github.com/ronaldmiranda/cloudflared-controller/internal/cloudflare"
	"github.com/ronaldmiranda/cloudflared-controller/internal/docker"
)

const dnsComment = "managed by cloudflared-controller"

// reconcileDNS ensures every desired hostname has a proxied CNAME pointing at
// the tunnel, and removes CNAMEs that point at the tunnel but no longer have
// a backing container. Records not targeting this tunnel are never touched.
//
// Only zones referenced by container labels are touched, so the API token can
// be scoped to just those zones. Zones are remembered for the lifetime of the
// process so a zone still gets swept after its last labeled container stops.
// DNS failures are logged, never fatal: a zone the token can't write to must
// not take down the tunnel routes of the others.
func (c *Controller) reconcileDNS(ctx context.Context, routes []docker.Route) {
	target := c.cf.TunnelDomain()

	desired := map[string]bool{}
	for _, r := range routes {
		if desired[r.Hostname] {
			continue
		}
		zone, err := c.cf.ZoneForHostname(ctx, r.Hostname)
		if err != nil {
			c.log.Error("skipping DNS for route", "hostname", r.Hostname, "error", err)
			continue
		}
		desired[r.Hostname] = true
		c.dnsZones[zone.ID] = *zone

		if err := c.ensureCNAME(ctx, zone, r.Hostname, target); err != nil {
			c.log.Error("ensuring DNS record failed", "hostname", r.Hostname, "zone", zone.Name, "error", err)
		}
	}

	for _, zone := range c.dnsZones {
		if err := c.sweepStale(ctx, &zone, target, desired); err != nil {
			c.log.Error("sweeping stale DNS records failed", "zone", zone.Name, "error", err)
		}
	}
}

func (c *Controller) ensureCNAME(ctx context.Context, zone *cloudflare.Zone, hostname, target string) error {
	existing, err := c.cf.ListDNSRecords(ctx, zone.ID, map[string]string{"name": hostname})
	if err != nil {
		return fmt.Errorf("listing DNS records for %s: %w", hostname, err)
	}

	want := cloudflare.DNSRecord{
		Type:    "CNAME",
		Name:    hostname,
		Content: target,
		Proxied: true,
		Comment: dnsComment,
		TTL:     1, // automatic
	}

	for _, rec := range existing {
		if rec.Type == "CNAME" && strings.EqualFold(rec.Content, target) && rec.Proxied {
			return nil // already correct
		}
		if rec.Type == "CNAME" && rec.Comment == dnsComment {
			want.ID = rec.ID
			c.log.Info("updating DNS record", "hostname", hostname, "target", target)
			return c.cf.UpdateDNSRecord(ctx, zone.ID, want)
		}
		// A record of another type or a CNAME we don't own occupies the name:
		// don't fight over it, just report.
		c.log.Error("hostname already has a DNS record not managed by this controller, skipping",
			"hostname", hostname, "type", rec.Type, "content", rec.Content)
		return nil
	}

	c.log.Info("creating DNS record", "hostname", hostname, "target", target)
	return c.cf.CreateDNSRecord(ctx, zone.ID, want)
}

func (c *Controller) sweepStale(ctx context.Context, zone *cloudflare.Zone, target string, desired map[string]bool) error {
	records, err := c.cf.ListDNSRecords(ctx, zone.ID, map[string]string{
		"type":    "CNAME",
		"content": target,
	})
	if err != nil {
		return fmt.Errorf("listing tunnel DNS records in zone %s: %w", zone.Name, err)
	}
	for _, rec := range records {
		if desired[rec.Name] {
			continue
		}
		if rec.Comment != dnsComment {
			// Points at the tunnel but was created manually; leave it alone.
			continue
		}
		c.log.Info("deleting stale DNS record", "hostname", rec.Name)
		if err := c.cf.DeleteDNSRecord(ctx, zone.ID, rec.ID); err != nil {
			return err
		}
	}
	return nil
}
