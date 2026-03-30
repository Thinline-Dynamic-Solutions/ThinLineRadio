/*
 * Block system admin UI on mobile browsers (same policy as scanner).
 */

import { inject } from '@angular/core';
import { CanActivateFn, Router } from '@angular/router';
import { isMobileRestrictedBrowser } from '../../components/rdio-scanner/mobile-browser.util';

export const mobileWebAdminGuard: CanActivateFn = () => {
    if (!isMobileRestrictedBrowser()) {
        return true;
    }
    return inject(Router).parseUrl('/');
};
