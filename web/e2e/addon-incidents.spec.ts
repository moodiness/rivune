import { expect, test } from "./fixtures/rivune";

test("administrator reviews and safely acknowledges a profile extension incident", async ({ page, rivune }) => {
  rivune.setInterfaceLanguage("fr");
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto("/#admin");
  await page.getByRole("button", { name: "Opérations" }).click();

  const center = page.getByRole("region", { name: "Incidents des extensions" });
  await expect(center).toBeVisible();
  const incident = center.getByRole("listitem");
  await expect(incident).toContainText("Cinema Index");
  await expect(incident).toContainText("Récupération");
  await expect(incident).toContainText("Disponibilité affectée");
  await expect(incident).toContainText("3 occurrences");
  await expect(incident).toContainText("Dernière requête réussie");
  await expect(incident).toContainText("Récupération non confirmée");
  await expect(center).not.toContainText(/https?:\/\/|token=|authorization|query|response body/i);

  await incident.getByRole("button", { name: "Accuser réception pour ce profil" }).click();
  const acknowledgement = await rivune.waitForRequest("/api/v1/operations/extension-incidents/88000000-0000-4000-8000-000000000010/acknowledgement", "POST");
  expect(acknowledgement.body).toBeUndefined();
  await expect(incident).toContainText("Réception confirmée");
  await expect(incident.getByRole("button", { name: "Accuser réception pour ce profil" })).toHaveCount(0);

  await page.setViewportSize({ width: 390, height: 844 });
  await expect.poll(() => page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await expect(center).toBeVisible();
});
