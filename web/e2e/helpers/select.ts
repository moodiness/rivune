import type { Locator } from "@playwright/test";

export function selectListbox(control: Locator) {
  return control.getAttribute("aria-controls").then((id) => {
    if (!id) throw new Error("Combobox is missing aria-controls");
    return control.page().locator(`[id=${JSON.stringify(id)}]`);
  });
}

export async function selectOption(control: Locator, value: string) {
  if (await control.getAttribute("aria-expanded") !== "true") await control.click();
  const listbox = await selectListbox(control);
  const options = listbox.getByRole("option");
  const index = await options.evaluateAll((elements, requestedValue) => elements.findIndex((element) => element.getAttribute("data-value") === requestedValue), value);
  if (index < 0) throw new Error(`Combobox option ${JSON.stringify(value)} was not found`);
  await options.nth(index).click();
}

export async function selectOptions(control: Locator) {
  if (await control.getAttribute("aria-expanded") !== "true") await control.click();
  const listbox = await selectListbox(control);
  return listbox.getByRole("option").evaluateAll((options) => options.map((option) => ({
    value: option.getAttribute("data-value") ?? "",
    label: option.textContent?.trim() ?? "",
  })));
}
