import { expect, type Page } from "@playwright/test";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));

export function adminPassword(): string {
  const env = fs.readFileSync(path.resolve(here, "../../.env"), "utf8");
  const m = env.match(/^SBC_BOOTSTRAP_ADMIN_PASSWORD=(.*)$/m);
  return m?.[1] ?? "admin12345";
}

export async function login(page: Page) {
  await page.goto("/login");
  await page.getByTestId("email").fill("admin@example.com");
  await page.getByTestId("password").fill(adminPassword());
  await page.getByTestId("login-submit").click();
  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
}

export const unique = (prefix: string) => `${prefix}-${Date.now().toString(36)}`;
