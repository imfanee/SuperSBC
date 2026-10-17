import { useState } from "react";
import { Link } from "react-router-dom";
import { ArrowRight, Building2, Check, Copy, Github, Linkedin, Mail, Phone, User } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Section, SectionHeading } from "@/components/marketing/sections";
import { usePageMeta } from "@/components/marketing/head";
import { site } from "@/site.config";

/** A detail with a copy button, because a phone number on a laptop is only
 * useful if it can be taken somewhere else. */
function Detail({
  icon: Icon,
  label,
  value,
  href,
}: {
  icon: typeof Mail;
  label: string;
  value: string;
  href?: string;
}) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1600);
    } catch {
      /* a browser that refuses the clipboard still shows the value */
    }
  };
  return (
    <div className="flex items-start gap-4 rounded-xl border bg-card p-5">
      <span className="grid h-10 w-10 shrink-0 place-items-center rounded-lg bg-primary/10 text-primary">
        <Icon className="h-5 w-5" />
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-xs uppercase tracking-wide text-muted-foreground">{label}</div>
        {href ? (
          <a
            href={href}
            target={href.startsWith("http") ? "_blank" : undefined}
            rel="noreferrer"
            className="break-words font-medium hover:text-primary"
          >
            {value}
          </a>
        ) : (
          <div className="break-words font-medium">{value}</div>
        )}
      </div>
      <Button variant="ghost" size="icon" aria-label={`Copy ${label}`} onClick={copy}>
        {copied ? <Check className="text-emerald-600" /> : <Copy />}
      </Button>
    </div>
  );
}

export function MarketingContactPage() {
  usePageMeta(
    "Contact",
    "Talk to the team behind SuperSBC about routing, billing, migration or a trial against your own traffic.",
  );
  const { contact } = site;
  const [form, setForm] = useState({ name: "", company: "", message: "" });
  const mailto = `mailto:${contact.email}?subject=${encodeURIComponent(
    `${site.name} enquiry${form.company ? " from " + form.company : ""}`,
  )}&body=${encodeURIComponent(
    `${form.message}\n\n--\n${form.name}${form.company ? "\n" + form.company : ""}`,
  )}`;

  return (
    <>
      <Section>
        <div className="grid gap-12 lg:grid-cols-[1fr_1fr] lg:gap-16">
          <div>
            <SectionHeading
              eyebrow="Contact"
              title="Talk to the person who built it"
              lead="No sales desk and no ticket queue. Questions about routing, billing behaviour, migration from another switch or a trial against your own traffic all reach the same inbox."
            />
            <div className="mt-10 space-y-3">
              <Detail icon={User} label="Name" value={contact.name} />
              {contact.role && <Detail icon={User} label="Role" value={contact.role} />}
              <Detail icon={Building2} label="Company" value={contact.company} />
              <Detail icon={Mail} label="Email" value={contact.email} href={`mailto:${contact.email}`} />
              <Detail
                icon={Phone}
                label="Phone"
                value={contact.phone}
                href={`tel:${contact.phone.replace(/[^+\d]/g, "")}`}
              />
              {contact.linkedin && (
                <Detail icon={Linkedin} label="LinkedIn" value={contact.linkedin} href={contact.linkedin} />
              )}
              <Detail icon={Github} label="GitHub" value={site.github} href={site.github} />
            </div>
          </div>

          <div className="lg:pt-10">
            <div className="rounded-2xl border bg-card p-6 sm:p-8">
              <h2 className="text-lg font-semibold">Send a message</h2>
              <p className="mt-2 text-sm text-muted-foreground">
                This opens your own mail client with the message ready, so nothing you type is stored on this
                site.
              </p>
              <div className="mt-6 space-y-4">
                <label className="block">
                  <span className="text-sm font-medium">Your name</span>
                  <input
                    className="mt-1.5 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
                    value={form.name}
                    onChange={(e) => setForm({ ...form, name: e.target.value })}
                    placeholder="Jane Okonkwo"
                    data-testid="contact-name"
                  />
                </label>
                <label className="block">
                  <span className="text-sm font-medium">Company</span>
                  <input
                    className="mt-1.5 w-full rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
                    value={form.company}
                    onChange={(e) => setForm({ ...form, company: e.target.value })}
                    placeholder="Carrier or reseller"
                  />
                </label>
                <label className="block">
                  <span className="text-sm font-medium">What do you need?</span>
                  <textarea
                    rows={5}
                    className="mt-1.5 w-full resize-y rounded-md border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-ring"
                    value={form.message}
                    onChange={(e) => setForm({ ...form, message: e.target.value })}
                    placeholder="Traffic volume, destinations, what you run today"
                    data-testid="contact-message"
                  />
                </label>
                <Button asChild className="w-full" size="lg">
                  <a href={mailto} data-testid="contact-send">
                    Open in my mail client <ArrowRight />
                  </a>
                </Button>
                <p className="text-center text-xs text-muted-foreground">
                  Or write directly to{" "}
                  <a className="underline underline-offset-2" href={`mailto:${contact.email}`}>
                    {contact.email}
                  </a>
                </p>
              </div>
            </div>
          </div>
        </div>
      </Section>

      <Section muted>
        <div className="flex flex-col items-center gap-5 text-center">
          <h2 className="text-balance text-2xl font-semibold tracking-tight">Already have an account?</h2>
          <p className="max-w-xl text-pretty text-muted-foreground">
            The operator portal holds your routing, rates, customers, carriers and every call record.
          </p>
          <Button asChild size="lg">
            <Link to={site.portalPath}>
              Log in <ArrowRight />
            </Link>
          </Button>
        </div>
      </Section>
    </>
  );
}
