// Double quotes still work, and an ESM-style single-quoted import is picked
// up too — both were broken for plain .js before.
const members = require("./routes/members");
import admin from './routes/admin';

// A dynamic require must resolve to nothing rather than a fabricated edge.
const which = process.env.ROUTE;
const dynamic = require(which);

module.exports = { members, admin, dynamic };
