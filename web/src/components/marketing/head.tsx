import { useEffect } from "react";
import { site } from "@/site.config";

/**
 * Per page title and description. The portal is a single page application, so
 * a share or a bookmark of a public page would otherwise carry the same title
 * everywhere (D-82).
 */
export function usePageMeta(title: string, description: string) {
  useEffect(() => {
    const previous = document.title;
    document.title = `${title} | ${site.name}`;
    const meta = document.querySelector('meta[name="description"]');
    const previousDescription = meta?.getAttribute("content") ?? "";
    meta?.setAttribute("content", description);
    return () => {
      document.title = previous;
      meta?.setAttribute("content", previousDescription);
    };
  }, [title, description]);
}
