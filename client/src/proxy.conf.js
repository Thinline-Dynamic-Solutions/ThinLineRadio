/*
 * *****************************************************************************
 * Copyright (C) 2019-2024 Chrystian Huot <chrystian@huot.qc.ca>
 *
 * This program is free software: you can redistribute it and/or modify
 * it under the terms of the GNU General Public License as published by
 * the Free Software Foundation, either version 3 of the License, or
 * (at your option) any later version.
 *
 * This program is distributed in the hope that it will be useful,
 * but WITHOUT ANY WARRANTY; without even the implied warranty of
 * MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
 * GNU General Public License for more details.
 *
 * You should have received a copy of the GNU General Public License
 * along with this program.  If not, see <http://www.gnu.org/licenses/>
 * ****************************************************************************
 */

'use strict';

const server = 'http://localhost:3000';

// Only proxy API/HTTP assets — do NOT blanket-proxy /** or the Angular
// live-reload websocket (/ng-cli-ws) gets swallowed and the page thrash-reloads.
// App WebSocket connects directly to :3000 in development (see getWebsocketUrl).
module.exports = [
    {
        context: [
            '/api',
            '/email-logo',
            '/ws',
        ],
        secure: false,
        target: server,
        changeOrigin: true,
    },
];
