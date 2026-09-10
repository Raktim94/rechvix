import type { SVGProps } from "react";

/** One hand-drawn outline set, matching the stroke style QuickAccess
 * established (24x24 viewBox, 1.8 stroke, round caps/joins) — kept as
 * plain inline SVG rather than pulling in an icon package, since every
 * other glyph in this app (Logo, QuickAccess) already follows that
 * convention and a mixed style would be more visible than the weight of
 * one more dependency would be worth. */
function Icon({ children, ...props }: SVGProps<SVGSVGElement>) {
  return (
    <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true" {...props}>
      {children}
    </svg>
  );
}

export const DashboardIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 13h6V4H4v9zM14 20h6v-9h-6v9zM14 4v4h6V4h-6zM4 20h6v-4H4v4z" />
  </Icon>
);

export const SalesIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 3h11l4 4v14H4z M15 3v5h5 M9 13h6 M9 17h6" />
  </Icon>
);

export const PurchasesIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M3 6h2l2.4 12h11.2L21 8H7 M9 21a1 1 0 100-2 1 1 0 000 2z M18 21a1 1 0 100-2 1 1 0 000 2z" />
  </Icon>
);

export const InventoryIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M3 8l9-5 9 5-9 5-9-5z M3 8v8l9 5 9-5V8 M12 13v8" />
  </Icon>
);

export const CatalogueIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M5 4h11l3 3v13H5z M16 4v3h3 M9 12h6 M9 16h6" />
  </Icon>
);

export const PricingIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 3v18 M17 6.5c0-1.4-1.6-2.5-5-2.5s-5 1.3-5 3 2 2.4 5 3 5 1.4 5 3-2 3-5 3-5-1.3-5-2.7" />
  </Icon>
);

export const ContactsIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 12a4 4 0 100-8 4 4 0 000 8z M4 21c1.5-4.5 5-6 8-6s6.5 1.5 8 6" />
  </Icon>
);

export const AccountingIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 4h16v4H4z M6 8v13 M18 8v13 M4 21h16 M9 12h1 M14 12h1 M9 16h1 M14 16h1" />
  </Icon>
);

export const GstIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M20 12a8 8 0 11-4.2-7.05 M20 4v5h-5" />
    <path d="M9 14l6-6 M9.3 9.3h.01 M14.7 14.7h.01" />
  </Icon>
);

export const ReportsIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 20V10 M10 20V4 M16 20v-7 M22 20H2" />
  </Icon>
);

export const IntegrationsIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M8 12a4 4 0 014-4h1a3 3 0 100-6 M16 12a4 4 0 01-4 4h-1a3 3 0 100 6" />
    <path d="M9 12H4 M20 12h-5" />
  </Icon>
);

export const BackupIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 3a9 9 0 108 13 M12 3v5l3-1" />
  </Icon>
);

export const SettingsIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 15a3 3 0 100-6 3 3 0 000 6z" />
    <path d="M19.4 13a7.6 7.6 0 000-2l2-1.5-2-3.5-2.3.7a7.6 7.6 0 00-1.7-1L15 3h-4l-.4 2.4a7.6 7.6 0 00-1.7 1L6.6 5.7l-2 3.5L6.6 11a7.6 7.6 0 000 2l-2 1.5 2 3.5 2.3-.7a7.6 7.6 0 001.7 1L11 21h4l.4-2.4a7.6 7.6 0 001.7-1l2.3.7 2-3.5-2-1.5z" />
  </Icon>
);

export const WalletIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M3 7a2 2 0 012-2h13a1 1 0 011 1v3 M3 7v11a2 2 0 002 2h14a1 1 0 001-1v-8a1 1 0 00-1-1H6a2 2 0 01-2-2z" />
    <path d="M16 15h2" />
  </Icon>
);

export const ArrowDownCircleIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 3a9 9 0 100 18 9 9 0 000-18z M12 8v7 M8.5 12.5L12 16l3.5-3.5" />
  </Icon>
);

export const ArrowUpCircleIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 3a9 9 0 100 18 9 9 0 000-18z M12 16V9 M8.5 11.5L12 8l3.5 3.5" />
  </Icon>
);

export const BoxIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M3 8l9-5 9 5-9 5-9-5z M3 8v8l9 5 9-5V8 M12 13v8" />
  </Icon>
);

export const AlertTriangleIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 4l9 16H3z M12 10v4 M12 17h.01" />
  </Icon>
);

export const ClockIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 3a9 9 0 100 18 9 9 0 000-18z M12 7v5l4 2" />
  </Icon>
);

export const WhatsAppIcon = (p: SVGProps<SVGSVGElement>) => (
  <svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" {...p}>
    <path d="M17.5 14.4c-.3-.1-1.7-.8-2-.9-.3-.1-.4-.1-.6.1-.2.3-.7.9-.9 1-.2.2-.3.2-.6.1-.9-.4-1.9-1-2.7-1.9-.7-.7-1.2-1.5-1.6-2.3-.1-.3 0-.4.1-.5l.5-.6c.1-.2.2-.3.1-.5-.1-.2-.6-1.5-.8-2-.2-.5-.4-.4-.6-.4h-.5c-.2 0-.5.1-.7.3-.2.3-1 1-1 2.3 0 1.4 1 2.7 1.1 2.9.1.2 1.9 3 4.7 4.1 2.7 1.1 2.7.7 3.2.7.5 0 1.6-.6 1.8-1.2.2-.6.2-1.1.2-1.2-.1-.1-.2-.2-.7-.4z" />
    <path d="M12 2a10 10 0 00-8.6 15.1L2 22l4.9-1.3A10 10 0 1012 2zm0 18.2a8.2 8.2 0 01-4.2-1.1l-.3-.2-3 .8.8-2.9-.2-.3A8.2 8.2 0 1112 20.2z" />
  </svg>
);

export const CommandIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M8 4a2 2 0 100 4h8a2 2 0 100-4M8 20a2 2 0 100-4h8a2 2 0 100 4M8 8v8M16 8v8" />
  </Icon>
);

export const StaffIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M17 20v-1.5a3.5 3.5 0 00-3.5-3.5h-5A3.5 3.5 0 005 18.5V20" />
    <path d="M9.5 11a3 3 0 100-6 3 3 0 000 6z" />
    <path d="M20 20v-1.5a3 3 0 00-2.2-2.9" />
    <path d="M16 4.6a3 3 0 010 5.8" />
  </Icon>
);

export const CalendarIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 5h16a1 1 0 011 1v14a1 1 0 01-1 1H4a1 1 0 01-1-1V6a1 1 0 011-1z" />
    <path d="M4 9h16 M8 3v4 M16 3v4 M8 13h.01 M12 13h.01 M16 13h.01 M8 17h.01 M12 17h.01" />
  </Icon>
);

export const EyeIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7z" />
    <path d="M12 15a3 3 0 100-6 3 3 0 000 6z" />
  </Icon>
);

export const EyeOffIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M3 3l18 18" />
    <path d="M10.6 5.2A9.9 9.9 0 0112 5c6.5 0 10 7 10 7a13.2 13.2 0 01-3 3.9M6.1 6.1C4 7.5 2 12 2 12s2.2 4.4 6.1 6.2a10 10 0 003.6.8" />
    <path d="M9.9 9.9a3 3 0 004.2 4.2" />
  </Icon>
);

export const SearchIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M11 19a8 8 0 100-16 8 8 0 000 16z M21 21l-4.35-4.35" />
  </Icon>
);

export const PlusIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M12 5v14M5 12h14" />
  </Icon>
);

export const BuildingIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M4 21V6a1 1 0 011-1h6a1 1 0 011 1v15" />
    <path d="M12 21V10a1 1 0 011-1h6a1 1 0 011 1v11" />
    <path d="M2 21h20" />
    <path d="M7.5 8h.01M7.5 12h.01M7.5 16h.01" />
  </Icon>
);

export const ChevronDownIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M6 9l6 6 6-6" />
  </Icon>
);

export const CheckIcon = (p: SVGProps<SVGSVGElement>) => (
  <Icon {...p}>
    <path d="M5 12l5 5L20 7" />
  </Icon>
);
