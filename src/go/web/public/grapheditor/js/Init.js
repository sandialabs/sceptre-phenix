/**
 * Copyright 2017-2021 National Technology & Engineering Solutions of Sandia, LLC (NTESS). 
 * Under the terms of Contract DE-NA0003525 with NTESS, the U.S. Government retains certain 
 * rights in this software.
 */

// urlParams is null when used for embedding
window.urlParams = window.urlParams || {};

// Public global variables
window.MAX_REQUEST_SIZE = window.MAX_REQUEST_SIZE  || 10485760;
window.MAX_AREA = window.MAX_AREA || 15000 * 15000;

// URLs for save and export
window.EXPORT_URL = window.EXPORT_URL || '/export';
window.SAVE_URL = window.SAVE_URL || '/save';
window.OPEN_URL = window.OPEN_URL || '/open';
window.RESOURCES_PATH = window.RESOURCES_PATH || 'resources';
window.RESOURCE_BASE = window.RESOURCE_BASE || window.RESOURCES_PATH + '/grapheditor';
window.STENCIL_PATH = window.STENCIL_PATH || 'stencils';
window.IMAGE_PATH = window.IMAGE_PATH || 'images';
window.STYLE_PATH = window.STYLE_PATH || 'styles';
window.CSS_PATH = window.CSS_PATH || 'styles';
window.OPEN_FORM = window.OPEN_FORM || 'open.html';

/**
 * Restores the stencil directory on image= styles.
 *
 * mxObjectCodec.encodeObject drops an image's directory when a diagram is
 * encoded, keeping stored XML independent of where phenix is served from. Every
 * path that decodes that XML back into the graph has to put the directory back,
 * or the cells render with a missing image. Container artwork ends in
 * _container; everything else lives with the virtual machines.
 *
 * Callers: js/Actions.js (import), js/Dialogs.js (Edit Diagram) and open.html,
 * which reaches this through window.parent. Keep it here so the three cannot
 * drift apart again.
 */
window.phenixRestoreStencilPaths = function(xml)
{
    if (xml == null)
    {
        return xml;
    }

    var stencilPath = window.STENCIL_PATH ||
        (window.parent != null ? window.parent.STENCIL_PATH : null);

    if (!stencilPath)
    {
        return xml;
    }

    return xml.replace(/image=([^;"]*)/g, function(match, path)
    {
        // Leave absolute references (http:, data:, ...) alone.
        if (/^[a-zA-Z][a-zA-Z0-9+.-]*:/.test(path))
        {
            return match;
        }

        var name = path.substring(path.lastIndexOf('/') + 1);

        if (name === '')
        {
            return match;
        }

        var dir = /_container\.[^.\/]+$/.test(name) ? '/containers/' : '/virtual_machines/';

        return 'image=' + stencilPath + dir + name;
    });
};

// Sets the base path, the UI language via URL param and configures the
// supported languages to avoid 404s. The loading of all core language
// resources is disabled as all required resources are in grapheditor.
// properties. Note that in this example the loading of two resource
// files (the special bundle and the default bundle) is disabled to
// save a GET request. This requires that all resources be present in
// each properties file since only one file is loaded.
window.mxBasePath = window.mxBasePath || '../../../src';
window.mxLanguage = window.mxLanguage || urlParams['lang'];
window.mxLanguages = window.mxLanguages || ['de', 'se'];
