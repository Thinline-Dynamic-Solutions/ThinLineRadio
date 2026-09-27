import { isMobileRestrictedBrowser } from './mobile-browser.util';

function withUA(ua: string, platform = 'Linux x86_64', maxTouchPoints = 0): void {
    Object.defineProperty(navigator, 'userAgent', { configurable: true, get: () => ua });
    Object.defineProperty(navigator, 'platform', { configurable: true, get: () => platform });
    Object.defineProperty(navigator, 'maxTouchPoints', { configurable: true, get: () => maxTouchPoints });
}

describe('isMobileRestrictedBrowser', () => {
    it('treats Chrome OS / Chromebox as a desktop scanner', () => {
        withUA('Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36');
        expect(isMobileRestrictedBrowser()).toBe(false);
    });

    it('does not send Chromebooks with Android in the UA to the mobile hub', () => {
        withUA('Mozilla/5.0 (Linux; Android 9; Pixelbook) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36 Chromebook');
        expect(isMobileRestrictedBrowser()).toBe(false);
    });

    it('still restricts phones', () => {
        withUA('Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Mobile Safari/537.36');
        expect(isMobileRestrictedBrowser()).toBe(true);
    });
});
