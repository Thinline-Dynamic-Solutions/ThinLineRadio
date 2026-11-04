/*
 * *****************************************************************************
 * Copyright (C) 2026 Thinline Dynamic Solutions
 *
 * Licensed under the GNU GPL v3 (or later). See LICENSE.
 * ****************************************************************************
 */

const STORAGE_KEY = 'tlr.listenLink';
const TOKEN_RE = /^[A-Za-z0-9]{6,32}$/;

/**
 * Share-link token for this tab. The server redirects /l/{token} to
 * /?link={token}; the token is kept for the rest of the tab's session so
 * in-app navigation and reloads keep the link's scope.
 */
export function listenLinkToken(): string | null {
    const fromUrl = new URLSearchParams(window.location.search).get('link');
    if (fromUrl && TOKEN_RE.test(fromUrl)) {
        sessionStorage.setItem(STORAGE_KEY, fromUrl);
        return fromUrl;
    }
    return sessionStorage.getItem(STORAGE_KEY);
}

export function clearListenLinkToken(): void {
    sessionStorage.removeItem(STORAGE_KEY);
}

/** Appends ?link= (or &link=) when a share link is active. */
export function withListenLink(url: string): string {
    const token = listenLinkToken();
    if (!token) {
        return url;
    }
    return `${url}${url.includes('?') ? '&' : '?'}link=${encodeURIComponent(token)}`;
}
