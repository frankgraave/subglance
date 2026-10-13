import type { ReactNode } from "react";
import { formatDateIso } from "../format/format";
import type { OfferedCertificate } from "./preview";

/*
 * Trusting one certificate on one monitor, as both monitor forms draw it.
 *
 * Two states share one block because they are one setting: the certificate a
 * failed Test it was offered, with the button that trusts it, and the
 * certificate already trusted, with the button that stops. Both can show at
 * once, when a trusted certificate was replaced and the test offers the new
 * one.
 *
 * There is no switch that turns verification off, and no field to type a
 * fingerprint into: a pin is taken from a certificate the server really
 * presented, so the only fingerprint the form can trust is one it has just
 * shown, beside the sentence that says to compare it. Typing one in is the
 * API's job (docs/using-subglance.md).
 *
 * Drawn from existing parts only (`.panel-settings` for the facts, which
 * breaks the 95-character fingerprint anywhere instead of pushing a phone's
 * drawer wide, `face-mono`, `.warn-note`, `.button`), so the entry
 * stylesheet does not grow for it.
 */

export type CertificateTrustProps = {
  /** The pin the form holds, or "" for none. */
  trusted: string;
  /** When the stored pin was set; absent for a pin not saved yet. */
  trustedAt?: string;
  /** What the last test was offered, when trusting it would let it pass. */
  offered?: OfferedCertificate;
  onTrust: (sha256: string) => void;
  onStop: () => void;
  /** Given to the Stop trusting button, so a refusal can focus it. */
  stopId: string;
  /** A refusal about the pin, placed under it, and the id it carries. */
  error?: ReactNode;
  errorId?: string;
  disabled?: boolean;
};

export function CertificateTrust({
  trusted,
  trustedAt,
  offered,
  onTrust,
  onStop,
  stopId,
  error,
  errorId,
  disabled = false,
}: CertificateTrustProps) {
  const offer = offered !== undefined && offered.sha256 !== trusted ? offered : undefined;
  if (trusted === "" && offer === undefined) return null;
  return (
    <>
      {trusted !== "" && (
        <div className="field">
          <dl className="panel-settings">
            <div>
              <dt className="field-label">Trusted certificate (SHA-256)</dt>
              <dd className="face-mono">{trusted}</dd>
            </div>
          </dl>
          <p className="field-help">
            {trustedAt !== undefined ? `Trusted on ${formatDateIso(trustedAt)}. ` : ""}
            The check passes while the server presents exactly this certificate.
            Its expiry still counts, and any other certificate fails the check.
          </p>
          <div className="button-row">
            <button
              type="button"
              className="button"
              id={stopId}
              name="trusted_cert_sha256"
              onClick={onStop}
              disabled={disabled}
              aria-describedby={errorId}
            >
              Stop trusting
            </button>
          </div>
          {error}
        </div>
      )}
      {offer !== undefined && <CertificateOffer certificate={offer} replaces={trusted !== ""} onTrust={onTrust} disabled={disabled} />}
    </>
  );
}

function CertificateOffer({
  certificate,
  replaces,
  onTrust,
  disabled,
}: {
  certificate: OfferedCertificate;
  replaces: boolean;
  onTrust: (sha256: string) => void;
  disabled: boolean;
}) {
  const selfSigned = certificate.issuer === certificate.subject;
  return (
    <div className="field" role="group" aria-label="Certificate the test was offered">
      <p className="field-help">
        {replaces
          ? "The server presents a different certificate from the one trusted. Trusting this one replaces it."
          : "Nothing on this host vouches for the certificate the server presented. Trusting it lets this monitor pass while the server presents exactly this certificate."}
      </p>
      <dl className="panel-settings">
        <div><dt className="field-label">Issued to</dt><dd>{certificate.subject}</dd></div>
        <div><dt className="field-label">Issued by</dt><dd>{selfSigned ? "Itself (self-signed)" : certificate.issuer}</dd></div>
        <div><dt className="field-label">Expires</dt><dd>{formatDateIso(certificate.not_after)}</dd></div>
        <div><dt className="field-label">SHA-256 fingerprint</dt><dd className="face-mono">{certificate.sha256}</dd></div>
      </dl>
      {/* Trust on first use: whatever answered the test is what gets
          trusted, so the comparison is the user's only defence against
          pinning a machine in the middle. A filled block, not helper text. */}
      <p className="warn-note">
        Compare this fingerprint with the one the service shows on its own certificate page before trusting it.
        Whatever answered this test is what gets trusted.
      </p>
      <div className="button-row">
        <button type="button" className="button" onClick={() => onTrust(certificate.sha256)} disabled={disabled}>
          Trust this certificate
        </button>
      </div>
    </div>
  );
}
