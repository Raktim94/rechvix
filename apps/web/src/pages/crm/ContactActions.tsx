import { MailIcon, PhoneIcon, WhatsAppIcon } from "../../components/icons";
import { whatsappDigits, type Opp } from "./crm";
import styles from "./CrmPage.module.css";

/** One-tap ways to reach the person behind a deal: call, WhatsApp, email. Only what the deal has. */
export function ContactActions({ opp }: { opp: Opp }) {
  const phone = opp.contact_phone;
  const digits = phone ? whatsappDigits(phone) : "";
  if (!phone && !opp.contact_email) return null;
  return (
    <div className={styles.cardActions}>
      {phone ? (
        <a className={styles.iconLink} href={`tel:${phone}`} aria-label={`Call ${phone}`} title="Call">
          <PhoneIcon />
        </a>
      ) : null}
      {digits ? (
        <a className={styles.iconLink} href={`https://wa.me/${digits}`} target="_blank" rel="noreferrer noopener" aria-label={`WhatsApp ${phone}`} title="WhatsApp">
          <WhatsAppIcon />
        </a>
      ) : null}
      {opp.contact_email ? (
        <a className={styles.iconLink} href={`mailto:${opp.contact_email}`} aria-label={`Email ${opp.contact_email}`} title="Email">
          <MailIcon />
        </a>
      ) : null}
    </div>
  );
}
