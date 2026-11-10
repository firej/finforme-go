// Stripe visible rows across nested branches and stop connectors at the last sibling.
window.AccountTree = {
  refresh: function(tree) {
    if (!tree) return;
    var sidebar = tree.classList.contains('sidebar-accounts');
    var nodeClass = sidebar ? 'stree-item' : 'ptree-node';
    var rowClass = sidebar ? 'stree-row' : 'ptree-row';
    var childrenClass = sidebar ? 'stree-children' : 'ptree-children';
    var searching = sidebar && tree.classList.contains('is-searching');
    var showHidden = document.body.classList.contains('show-hidden');
    var index = 0;

    function visit(group) {
      var nodes = Array.from(group.children).filter(function(node) {
        return node.classList.contains(nodeClass) && !node.hidden &&
          (sidebar || showHidden || !node.classList.contains('is-hidden'));
      });
      nodes.forEach(function(node, i) {
        node.classList.toggle('is-last-visible', i === nodes.length - 1);
        Array.from(node.children).forEach(function(child) {
          if (child.classList.contains(rowClass)) {
            child.classList.toggle('is-even', index++ % 2 === 1);
          } else if (child.classList.contains(childrenClass) &&
                     (searching || child.style.display !== 'none')) {
            visit(child);
          }
        });
      });
    }

    visit(tree);
  }
};
