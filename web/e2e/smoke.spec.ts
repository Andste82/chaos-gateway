import { expect, test } from '@playwright/test';

test('the app loads and shows the product name', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: 'Chaos Gateway' })).toBeVisible();
});
