import fs from 'fs';
import os from 'os';
import path from 'path';

import { expect } from '@playwright/test';
import which from 'which';

import { NavPage } from '../e2e/pages/nav-page';
import { PreferencesPage } from '../e2e/pages/preferences';

import { spawnFile } from '@pkg/utils/childProcess';
import { Log } from '@pkg/utils/logging';

import type { Page } from '@playwright/test';
import type { Rectangle } from 'electron';

interface ScreenshotsOptions {
  directory: string;
  log:       Log;
}

export class Screenshots {
  // used by Mac api
  private appBundleTitle = 'Electron';

  protected windowTitle = '';
  private static screenshotIndex = 0;
  readonly page:      Page;
  readonly directory: string;
  readonly log:       Log;

  constructor(page: Page, opt: ScreenshotsOptions) {
    this.page = page;
    const { directory, log } = opt;

    this.directory = path.resolve(import.meta.dirname, 'output', os.platform(), directory);
    this.log = log;
  }

  protected buildPath(title: string): string {
    return path.resolve(this.directory, `${ Screenshots.screenshotIndex++ }_${ title }.png`);
  }

  protected async createScreenshotsDirectory() {
    if (!this.directory) {
      return;
    }

    await fs.promises.mkdir(
      this.directory,
      { recursive: true },
    );
  }

  /**
   * @param bounds The screen region the window occupies.  When given, capture
   *   the screen instead of the window, so that windows stacked on top of it
   *   are included.
   */
  protected async screenshot(title: string, bounds?: Rectangle) {
    const outPath = this.buildPath(title);

    try {
      switch (process.platform) {
      case 'darwin':
        await this.screenshotDarwin(outPath, bounds);
        break;
      case 'win32':
        await this.screenshotWindows(outPath, bounds);
        break;
      default:
        await this.screenshotLinux(outPath, bounds);
      }
    } catch (e) {
      console.error('Failed to take screenshot', { error: e });
      process.exit(1);
    }
  }

  protected async screenshotDarwin(outPath: string, bounds?: Rectangle) {
    if (bounds) {
      // `-l` composites the window contents, and the shadow we drop to keep the
      // image window-sized is all that separates overlapping windows.
      const { x, y, width, height } = bounds;

      await spawnFile('screencapture', ['-R', `${ x },${ y },${ width },${ height }`, outPath], { stdio: this.log });

      return;
    }

    const { stdout: windowId, stderr } = await spawnFile('GetWindowID', [this.appBundleTitle, this.windowTitle], { stdio: 'pipe' });

    if (!windowId) {
      throw new Error(`Failed to find window ID for ${ this.windowTitle }: ${ stderr || '(no stderr)' }`);
    }

    await spawnFile('screencapture', ['-a', '-o', '-l', windowId.trim(), outPath], { stdio: this.log });
  }

  protected async screenshotWindows(outPath: string, bounds?: Rectangle) {
    const script = path.resolve(import.meta.dirname, 'screenshot.ps1');
    const args = ['-ExecutionPolicy', 'Bypass', script, '-FilePath', outPath, '-Title', `'${ this.windowTitle }'`];

    // Raising the window would hide the windows stacked on top of it.
    if (!bounds) {
      args.push('-Foreground');
    }
    await spawnFile('powershell.exe', args, { stdio: this.log });
  }

  protected async screenshotLinux(outPath: string, bounds?: Rectangle) {
    const args: string[] = [];

    if (bounds) {
      // A window capture would exclude windows stacked on top, so crop the root
      // window to the region the window occupies.
      const { x, y, width, height } = bounds;

      args.push('-window', 'root', '-crop', `${ width }x${ height }+${ x }+${ y }`);
    } else {
      args.push('-window', await this.findLinuxWindowId());
    }
    args.push(outPath);

    // If `gm` is available, use `gm import`; otherwise, use `import`.
    if (await (which('gm', { nothrow: true }))) {
      await spawnFile('gm', ['import', ...args], { stdio: this.log });
    } else {
      await spawnFile('import', args, { stdio: this.log });
    }
  }

  protected async findLinuxWindowId(): Promise<string> {
    // Find the target window; note that this is a child window of the window
    // frame, so we can't use it directly.
    let windowId;
    let { stdout } = await spawnFile('xwininfo', ['-name', this.windowTitle, '-tree'], { stdio: 'pipe' });

    // Walk up the parents of the current window, until the parent is the root window.
    while (true) {
      this.log.log(stdout);
      ([, windowId] = /xwininfo: Window id: (0x[0-9a-f]+)/i.exec(stdout) ?? []);
      const [, parentId, rest] = /Parent window id: (0x[0-9a-f]+)(.*)/i.exec(stdout) ?? [];

      if (!parentId || rest.includes('(the root window)')) {
        break;
      }
      ({ stdout } = await spawnFile('xwininfo', ['-id', parentId, '-tree'], { stdio: 'pipe' }));
    }
    if (!windowId) {
      throw new Error(`Failed to find window ID for ${ this.windowTitle }`);
    }

    return windowId;
  }
}

export class MainWindowScreenshots extends Screenshots {
  constructor(page: Page, opt: ScreenshotsOptions) {
    super(page, opt);
    this.windowTitle = 'Rancher Desktop';
  }

  async take(tabName: Parameters<NavPage['navigateTo']>[0], navPage?: NavPage, timeout?: number): Promise<void>;
  async take(screenshotName: string, bounds?: Rectangle): Promise<void>;
  async take(name: string, navPageOrBounds?: NavPage | Rectangle, timeout = 200) {
    const navPage = navPageOrBounds instanceof NavPage ? navPageOrBounds : undefined;
    const bounds = navPageOrBounds instanceof NavPage ? undefined : navPageOrBounds;

    if (navPage) {
      await navPage.navigateTo(name as Parameters<NavPage['navigateTo']>[0]);
      await this.page.waitForTimeout(timeout);
    }

    await this.createScreenshotsDirectory();
    await this.screenshot(name, bounds);
  }
}

export class PreferencesScreenshots extends Screenshots {
  readonly preferencePage: PreferencesPage;

  constructor(page: Page, preferencePage: PreferencesPage, opt: ScreenshotsOptions) {
    super(page, opt);
    this.preferencePage = preferencePage;
    this.windowTitle = 'Rancher Desktop - Preferences';
  }

  async take(tabName: string, subTabName?: string) {
    const tab = (this.preferencePage as any)[tabName];

    await tab.nav.click();
    await expect(tab.nav).toHaveClass('preferences-nav-item active');
    const path = subTabName ? `${ tabName }_${ subTabName }` : tabName;

    await this.createScreenshotsDirectory();
    await this.screenshot(path);
  }
}

// If needed, set the screen resolution in CI.
await (async function() {
  if (!process.env.CI) {
    return;
  }
  switch (process.platform) {
  case 'win32': {
    const script = path.resolve(import.meta.dirname, 'set-display-resolution.ps1');
    await spawnFile(
      'powershell.exe',
      ['-ExecutionPolicy', 'Bypass', script],
      { stdio: 'inherit' });
  }
  }
})();
