/**
 * Everything the public site says about the product and who runs it, in one
 * place (D-82). The marketing pages read from here so a name, a number or a
 * claim is changed once and never drifts between pages.
 */
export const site = {
  name: "SuperSBC",
  tagline: "A session border controller with prepaid wholesale billing",
  summary:
    "SIP security, least cost routing and per-second prepaid billing in one system you run yourself. FreeSWITCH carries the media, a Go control plane decides every call, and nothing bills twice.",
  portalPath: "/login",
  repo: "https://github.com/imfanee/SuperSBC",
  github: "https://github.com/imfanee/",
  contact: {
    name: "Faisal Hanif",
    role: "", // shown under the name when set, left empty on purpose
    company: "AI Technologies",
    email: "imfanee@gmail.com",
    phone: "+44-7301082737",
    linkedin: "https://www.linkedin.com/in/faisal-hanif-ai-telecom/",
  },
  /** The stack a buyer is actually deploying. */
  stack: [
    { name: "FreeSWITCH 1.10", role: "SIP and media" },
    { name: "Go", role: "control plane and billing" },
    { name: "PostgreSQL", role: "system of record" },
    { name: "Redis", role: "hot cache and counters" },
    { name: "React", role: "admin portal" },
  ],
} as const;
