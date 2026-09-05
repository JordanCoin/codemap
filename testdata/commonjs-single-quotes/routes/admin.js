const layout = require('../services/layoutInputService');

module.exports = function admin() {
  return layout.build();
};
