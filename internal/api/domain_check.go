package api

import (
	"context"
	"strconv"

	"github.com/frankgraave/subglance/internal/checker"
	"github.com/frankgraave/subglance/internal/store"
)

// validateDomainWarnDays applies the column's range. Zero is accepted: for a
// domain monitor it means "never warn, only fail once the registration has
// expired", and unlike ssl_warn_days nothing turns it back into a default.
func validateDomainWarnDays(days *int) problem {
	if days == nil {
		return problem{}
	}
	if *days < 0 || *days > 365 {
		return fieldProblem("domain_warn_days", "domain_warn_days must be between 0 and 365")
	}
	return problem{}
}

// domainTypeProblem refuses a domain_warn_days on a monitor of another type,
// and a domain monitor checked more often than every six hours. The interval
// floor is the schema's too; refusing it here names the field and the reason.
func domainTypeProblem(typ string, intervalS int, warnDays *int) problem {
	if typ != store.TypeDomain {
		if warnDays != nil {
			return fieldProblem("domain_warn_days", "domain_warn_days applies only to domain monitors")
		}
		return problem{}
	}
	if intervalS != 0 && intervalS < store.MinDomainIntervalS {
		return fieldProblem("interval_s", "a domain monitor is checked at most every 6 hours (interval_s "+
			strconv.Itoa(store.MinDomainIntervalS)+" or more): a registration date moves once a year, "+
			"and registries limit clients that ask often")
	}
	return problem{}
}

// validateDomainTarget accepts a domain name a registry holds, or a name under
// one: www.example.co.uk is checked as example.co.uk.
func validateDomainTarget(target string, bad func(string) problem) problem {
	if _, err := checker.RegisteredDomain(target); err != nil {
		return bad("a domain monitor takes a registered domain name such as example.com: " + err.Error())
	}
	return problem{}
}

// applyDomainPatch applies a PATCH's domain settings once the type and the
// interval are settled. A monitor that becomes a domain monitor without a
// threshold gets the default one; one that stops being a domain monitor loses
// its threshold, which means nothing to another type.
func applyDomainPatch(m *store.Monitor, wasType string, req patchMonitorRequest) problem {
	if p := validateDomainWarnDays(req.DomainWarnDays); !p.ok() {
		return p
	}
	switch {
	case m.Type != store.TypeDomain:
		if req.DomainWarnDays != nil {
			return fieldProblem("domain_warn_days", "domain_warn_days applies only to domain monitors")
		}
		m.DomainWarnDays = 0
		return problem{}
	case req.DomainWarnDays != nil:
		m.DomainWarnDays = *req.DomainWarnDays
	case wasType != store.TypeDomain:
		m.DomainWarnDays = checker.DefaultDomainWarnDays
	}
	if req.IntervalS != nil || req.Type != nil {
		return domainTypeProblem(m.Type, m.IntervalS, nil)
	}
	return problem{}
}

// applyUnknownCheck reports a domain monitor whose latest check could not
// find out as "unknown", with the reason in place of an error.
func (s *Server) applyUnknownCheck(ctx context.Context, monitorID int64, resp *monitorResponse) {
	c, found, err := s.db.LatestUnknownCheck(ctx, monitorID)
	if err != nil {
		s.log.Error("latest unknown check", "monitor_id", monitorID, "error", err)
		return
	}
	if !found {
		return
	}
	at := c.At
	resp.Status = "unknown"
	resp.LastCheck = &at
	resp.LatencyMS = 0
	resp.StatusCode = 0
	resp.Error = c.Reason
	resp.FailureKind = string(checker.FailUnknown)
}
