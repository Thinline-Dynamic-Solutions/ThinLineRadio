/*
 * Mobile web policy: scanner UI is desktop/native-only; mobile browsers get account hub only.
 */

export function isMobileRestrictedBrowser(): boolean {
    if (typeof navigator === 'undefined' || typeof window === 'undefined') {
        return false;
    }
    const ua = navigator.userAgent || '';
    if (/Android|webOS|iPhone|iPod|BlackBerry|IEMobile|Opera Mini/i.test(ua)) {
        return true;
    }
    if (/iPad/i.test(ua)) {
        return true;
    }
    // iPadOS 13+ may report as Mac with touch
    const nav = navigator as Navigator & { maxTouchPoints?: number };
    if (navigator.platform === 'MacIntel' && nav.maxTouchPoints && nav.maxTouchPoints > 1) {
        return true;
    }
    return false;
}
