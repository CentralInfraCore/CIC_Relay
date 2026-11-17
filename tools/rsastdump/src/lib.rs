//! rsastdump parses Rust source files with `syn` and reports their items as
//! JSON. It is the syntactic front end of `tools/rs.meta.gen.py`, the Rust
//! counterpart of `tools/goastdump`: the Python side decides what counts as an
//! external reference and writes the companion YAML, this crate only reports
//! what the syntax tree says.
//!
//! No name resolution or type checking happens, so it runs offline and without
//! the dependencies of the code it reads. Paths are expanded through the `use`
//! declarations in scope (`use serde::Serialize;` turns `Serialize` into
//! `serde::Serialize`) and a path whose first segment is a module declared in
//! the same scope gets a `self::` prefix; everything else is reported as
//! written. A method call is attributed to a type only where the syntax states
//! it (`self`, a typed parameter, `let x: T`, `let x = T { .. }`); untyped
//! method calls are not reported at all.

use std::collections::{HashMap, HashSet};

use quote::ToTokens;
use serde::Serialize;
use syn::punctuated::Punctuated;
use syn::visit::{self, Visit};

/// The result for one input file.
#[derive(Debug, Default, Serialize)]
pub struct FileDump {
    /// The path as given on the command line.
    pub file: String,
    /// The inner (`//!`) documentation of the file.
    pub inner_doc: String,
    /// Names of the modules declared at the top level of the file
    /// (`mod x;` and `mod x { .. }`).
    pub mods: Vec<String>,
    /// Every item found, nested inline modules included, in source order.
    pub items: Vec<Item>,
    /// The parse error, if the file could not be read or parsed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub error: Option<String>,
}

/// One item of a Rust file.
#[derive(Debug, Default, Clone, Serialize)]
pub struct Item {
    /// The identifier; for an `impl` block "`Trait` for `Type`" or "`Type`".
    pub name: String,
    /// One of: struct enum union trait fn method impl type const static macro mod.
    pub kind: String,
    /// Inline modules the item is nested in, outermost first.
    pub mod_path: Vec<String>,
    /// True under `#[cfg(test)]` or for a `#[test]`-style function.
    pub test: bool,
    /// `pub`, `pub(crate)`, `pub(super)`, `pub(in …)` or `private`; absent
    /// where the language fixes it (trait impl methods).
    #[serde(skip_serializing_if = "Option::is_none")]
    pub visibility: Option<String>,
    /// The outer (`///`) documentation, whitespace collapsed.
    pub doc: String,
    /// For a method or an impl: the self type as written, without generics.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub receiver: Option<String>,
    /// For a trait impl and its methods: the trait path, expanded through `use`.
    #[serde(rename = "trait", skip_serializing_if = "Option::is_none")]
    pub trait_path: Option<String>,
    /// Paths of two or more segments (after `use` expansion) the item mentions,
    /// first occurrence order, without duplicates.
    pub refs: Vec<String>,
    /// Called functions and typed method calls, as paths.
    pub calls: Vec<String>,
    /// Enum only: variant names.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub variants: Option<Vec<String>>,
    /// Trait only: method names, sorted.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub methods: Option<Vec<String>>,
    /// Trait only: supertrait paths.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub supertraits: Option<Vec<String>>,
    /// Struct, enum, union: the `#[derive(..)]` paths.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub derives: Option<Vec<String>>,
}

/// The names one module's item list brings into scope.
#[derive(Debug, Default)]
struct Scope {
    uses: HashMap<String, Vec<String>>,
    mods: HashSet<String>,
}

impl Scope {
    fn collect(items: &[syn::Item]) -> Self {
        let mut s = Self::default();
        for it in items {
            match it {
                syn::Item::Use(u) => s.add_use(&mut Vec::new(), &u.tree),
                syn::Item::ExternCrate(e) => {
                    let name = e.rename.as_ref().map_or(&e.ident, |(_, r)| r);
                    if name != "_" {
                        s.uses.insert(name.to_string(), vec![e.ident.to_string()]);
                    }
                }
                syn::Item::Mod(m) => {
                    s.mods.insert(m.ident.to_string());
                }
                _ => {}
            }
        }
        s
    }

    fn add_use(&mut self, prefix: &mut Vec<String>, tree: &syn::UseTree) {
        match tree {
            syn::UseTree::Path(p) => {
                prefix.push(p.ident.to_string());
                self.add_use(prefix, &p.tree);
                prefix.pop();
            }
            syn::UseTree::Name(n) => {
                if n.ident == "self" {
                    if let Some(last) = prefix.last() {
                        self.uses.insert(last.clone(), prefix.clone());
                    }
                } else {
                    let mut full = prefix.clone();
                    full.push(n.ident.to_string());
                    self.uses.insert(n.ident.to_string(), full);
                }
            }
            syn::UseTree::Rename(r) => {
                if r.rename == "_" {
                    return;
                }
                let mut full = prefix.clone();
                if r.ident != "self" {
                    full.push(r.ident.to_string());
                }
                self.uses.insert(r.rename.to_string(), full);
            }
            syn::UseTree::Group(g) => {
                for t in &g.items {
                    self.add_use(prefix, t);
                }
            }
            syn::UseTree::Glob(_) => {}
        }
    }
}

/// Resolution context: the scopes of the enclosing modules (innermost last)
/// and the self type of the enclosing `impl` / `trait`, if any.
struct Ctx<'a> {
    scopes: &'a [Scope],
    self_ty: Option<&'a str>,
}

impl Ctx<'_> {
    fn resolve_segs(&self, mut segs: Vec<String>) -> Vec<String> {
        let Some(first) = segs.first() else {
            return segs;
        };
        if first == "Self" {
            if let Some(st) = self.self_ty {
                let mut out: Vec<String> = st.split("::").map(str::to_string).collect();
                out.extend(segs.drain(1..));
                return out;
            }
            return segs;
        }
        for scope in self.scopes.iter().rev() {
            if let Some(full) = scope.uses.get(first) {
                let mut out = full.clone();
                out.extend(segs.drain(1..));
                return out;
            }
            if segs.len() > 1 && scope.mods.contains(first) {
                segs.insert(0, "self".to_string());
                return segs;
            }
        }
        segs
    }

    fn resolve(&self, path: &syn::Path) -> String {
        let segs = path.segments.iter().map(|s| s.ident.to_string()).collect();
        self.resolve_segs(segs).join("::")
    }

    /// Names the type an expression of type `ty` has, if it is a plain path.
    fn type_name(&self, ty: &syn::Type) -> Option<String> {
        match ty {
            syn::Type::Path(p) if p.qself.is_none() => Some(self.resolve(&p.path)),
            syn::Type::Reference(r) => self.type_name(&r.elem),
            syn::Type::Paren(p) => self.type_name(&p.elem),
            syn::Type::Group(g) => self.type_name(&g.elem),
            _ => None,
        }
    }
}

/// Gathers refs and calls from a subtree.
struct Collector<'a> {
    ctx: &'a Ctx<'a>,
    refs: Vec<String>,
    calls: Vec<String>,
    seen_refs: HashSet<String>,
    seen_calls: HashSet<String>,
    locals: HashMap<String, String>,
}

impl<'a> Collector<'a> {
    fn new(ctx: &'a Ctx<'a>) -> Self {
        Self {
            ctx,
            refs: Vec::new(),
            calls: Vec::new(),
            seen_refs: HashSet::new(),
            seen_calls: HashSet::new(),
            locals: HashMap::new(),
        }
    }

    fn add_ref(&mut self, r: String) {
        if r.contains("::") && self.seen_refs.insert(r.clone()) {
            self.refs.push(r);
        }
    }

    fn add_call(&mut self, c: String) {
        if self.seen_calls.insert(c.clone()) {
            self.calls.push(c);
        }
    }

    fn params(&mut self, sig: &syn::Signature) {
        for arg in &sig.inputs {
            if let syn::FnArg::Typed(pt) = arg {
                self.bind(&pt.pat, &pt.ty);
            }
        }
    }

    fn bind(&mut self, pat: &syn::Pat, ty: &syn::Type) {
        if let syn::Pat::Ident(id) = pat {
            if let Some(t) = self.ctx.type_name(ty) {
                self.locals.insert(id.ident.to_string(), t);
            }
        }
    }

    fn receiver_type(&self, e: &syn::Expr) -> Option<String> {
        match e {
            syn::Expr::Path(p) if p.qself.is_none() => {
                let id = p.path.get_ident()?;
                if id == "self" {
                    return self.ctx.self_ty.map(str::to_string);
                }
                self.locals.get(&id.to_string()).cloned()
            }
            syn::Expr::Paren(p) => self.receiver_type(&p.expr),
            syn::Expr::Reference(r) => self.receiver_type(&r.expr),
            _ => None,
        }
    }

    fn finish(self) -> (Vec<String>, Vec<String>) {
        (self.refs, self.calls)
    }
}

impl<'ast> Visit<'ast> for Collector<'_> {
    // `pub(in crate::x)` names a scope, not something the item uses.
    fn visit_visibility(&mut self, _: &'ast syn::Visibility) {}

    fn visit_path(&mut self, p: &'ast syn::Path) {
        let r = self.ctx.resolve(p);
        self.add_ref(r);
        visit::visit_path(self, p);
    }

    fn visit_expr_call(&mut self, c: &'ast syn::ExprCall) {
        if let syn::Expr::Path(p) = &*c.func {
            if p.qself.is_none() {
                let r = self.ctx.resolve(&p.path);
                self.add_call(r);
            }
        }
        visit::visit_expr_call(self, c);
    }

    fn visit_expr_method_call(&mut self, m: &'ast syn::ExprMethodCall) {
        if let Some(t) = self.receiver_type(&m.receiver) {
            self.add_call(format!("{t}::{}", m.method));
        }
        visit::visit_expr_method_call(self, m);
    }

    fn visit_local(&mut self, l: &'ast syn::Local) {
        match &l.pat {
            syn::Pat::Type(pt) => self.bind(&pt.pat, &pt.ty),
            syn::Pat::Ident(id) => {
                if let Some(init) = &l.init {
                    if let syn::Expr::Struct(s) = &*init.expr {
                        let t = self.ctx.resolve(&s.path);
                        self.locals.insert(id.ident.to_string(), t);
                    }
                }
            }
            _ => {}
        }
        visit::visit_local(self, l);
    }

    fn visit_macro(&mut self, m: &'ast syn::Macro) {
        visit::visit_macro(self, m);
        // Most invocations (format!, vec!, assert_eq!, json! …) take
        // comma-separated expressions; read those, ignore the rest.
        if let Ok(args) =
            m.parse_body_with(Punctuated::<syn::Expr, syn::Token![,]>::parse_terminated)
        {
            for a in &args {
                self.visit_expr(a);
            }
        }
    }
}

fn doc_text(attrs: &[syn::Attribute], style_inner: bool) -> String {
    let mut parts = Vec::new();
    for a in attrs {
        let inner = matches!(a.style, syn::AttrStyle::Inner(_));
        if inner != style_inner || !a.path().is_ident("doc") {
            continue;
        }
        if let syn::Meta::NameValue(nv) = &a.meta {
            if let syn::Expr::Lit(syn::ExprLit {
                lit: syn::Lit::Str(s),
                ..
            }) = &nv.value
            {
                parts.push(s.value());
            }
        }
    }
    parts
        .join("\n")
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ")
}

fn is_test(attrs: &[syn::Attribute]) -> bool {
    attrs.iter().any(|a| {
        let p = a.path();
        if p.is_ident("cfg") {
            return a
                .meta
                .require_list()
                .is_ok_and(|l| l.tokens.to_string().trim() == "test");
        }
        p.segments.last().is_some_and(|s| s.ident == "test")
    })
}

fn visibility(v: &syn::Visibility) -> String {
    match v {
        syn::Visibility::Public(_) => "pub".to_string(),
        syn::Visibility::Restricted(r) => {
            let path = r.path.to_token_stream().to_string().replace(' ', "");
            if r.in_token.is_some() {
                format!("pub(in {path})")
            } else {
                format!("pub({path})")
            }
        }
        syn::Visibility::Inherited => "private".to_string(),
    }
}

/// The self type of an impl as written, generics and references dropped.
fn type_short(ty: &syn::Type) -> String {
    match ty {
        syn::Type::Path(p) if p.qself.is_none() => path_short(&p.path),
        syn::Type::Reference(r) => type_short(&r.elem),
        syn::Type::Paren(p) => type_short(&p.elem),
        syn::Type::Group(g) => type_short(&g.elem),
        other => other.to_token_stream().to_string().replace(' ', ""),
    }
}

fn path_short(p: &syn::Path) -> String {
    p.segments
        .iter()
        .map(|s| s.ident.to_string())
        .collect::<Vec<_>>()
        .join("::")
}

/// Walks the item lists of a file, keeping the module scopes.
#[derive(Default)]
struct Dumper {
    scopes: Vec<Scope>,
    mod_path: Vec<String>,
    test: bool,
    items: Vec<Item>,
}

impl Dumper {
    fn walk(&mut self, items: &[syn::Item]) {
        self.scopes.push(Scope::collect(items));
        for it in items {
            self.item(it);
        }
        self.scopes.pop();
    }

    fn base(&self, name: String, kind: &str, attrs: &[syn::Attribute]) -> Item {
        Item {
            name,
            kind: kind.to_string(),
            mod_path: self.mod_path.clone(),
            test: self.test || is_test(attrs),
            doc: doc_text(attrs, false),
            ..Item::default()
        }
    }

    fn derives(ctx: &Ctx<'_>, attrs: &[syn::Attribute]) -> Vec<String> {
        let mut out = Vec::new();
        for a in attrs.iter().filter(|a| a.path().is_ident("derive")) {
            if let Ok(paths) =
                a.parse_args_with(Punctuated::<syn::Path, syn::Token![,]>::parse_terminated)
            {
                out.extend(paths.iter().map(|p| ctx.resolve(p)));
            }
        }
        out
    }

    fn item(&mut self, it: &syn::Item) {
        if let syn::Item::Mod(m) = it {
            let mut o = self.base(m.ident.to_string(), "mod", &m.attrs);
            o.visibility = Some(visibility(&m.vis));
            let test = o.test;
            self.items.push(o);
            if let Some((_, content)) = &m.content {
                let saved = self.test;
                self.test = test;
                self.mod_path.push(m.ident.to_string());
                self.walk(content);
                self.mod_path.pop();
                self.test = saved;
            }
            return;
        }
        let out = self.objects(it);
        self.items.extend(out);
    }

    #[allow(clippy::too_many_lines)] // one arm per item kind reads best as one match
    fn objects(&self, it: &syn::Item) -> Vec<Item> {
        let mut out = Vec::new();
        let ctx = Ctx {
            scopes: &self.scopes,
            self_ty: None,
        };
        let mut k = Collector::new(&ctx);
        let obj = match it {
            syn::Item::Fn(f) => {
                k.params(&f.sig);
                k.visit_item_fn(f);
                let mut o = self.base(f.sig.ident.to_string(), "fn", &f.attrs);
                o.visibility = Some(visibility(&f.vis));
                Some(o)
            }
            syn::Item::Struct(s) => {
                k.visit_item_struct(s);
                let mut o = self.base(s.ident.to_string(), "struct", &s.attrs);
                o.visibility = Some(visibility(&s.vis));
                let d = Self::derives(&ctx, &s.attrs);
                for r in &d {
                    k.add_ref(r.clone());
                }
                o.derives = Some(d);
                Some(o)
            }
            syn::Item::Enum(e) => {
                k.visit_item_enum(e);
                let mut o = self.base(e.ident.to_string(), "enum", &e.attrs);
                o.visibility = Some(visibility(&e.vis));
                o.variants = Some(e.variants.iter().map(|v| v.ident.to_string()).collect());
                let d = Self::derives(&ctx, &e.attrs);
                for r in &d {
                    k.add_ref(r.clone());
                }
                o.derives = Some(d);
                Some(o)
            }
            syn::Item::Union(u) => {
                k.visit_item_union(u);
                let mut o = self.base(u.ident.to_string(), "union", &u.attrs);
                o.visibility = Some(visibility(&u.vis));
                let d = Self::derives(&ctx, &u.attrs);
                for r in &d {
                    k.add_ref(r.clone());
                }
                o.derives = Some(d);
                Some(o)
            }
            syn::Item::Trait(t) => {
                let name = t.ident.to_string();
                let tctx = Ctx {
                    scopes: &self.scopes,
                    self_ty: Some(&name),
                };
                let mut tk = Collector::new(&tctx);
                tk.visit_item_trait(t);
                let mut o = self.base(name.clone(), "trait", &t.attrs);
                o.visibility = Some(visibility(&t.vis));
                let mut methods: Vec<String> = t
                    .items
                    .iter()
                    .filter_map(|ti| match ti {
                        syn::TraitItem::Fn(f) => Some(f.sig.ident.to_string()),
                        _ => None,
                    })
                    .collect();
                methods.sort();
                o.methods = Some(methods);
                o.supertraits = Some(
                    t.supertraits
                        .iter()
                        .filter_map(|b| match b {
                            syn::TypeParamBound::Trait(tb) => Some(tctx.resolve(&tb.path)),
                            _ => None,
                        })
                        .collect(),
                );
                (o.refs, o.calls) = tk.finish();
                out.push(o);
                None
            }
            syn::Item::Impl(i) => {
                out = self.impl_block(i);
                None
            }
            syn::Item::Type(t) => {
                k.visit_item_type(t);
                let mut o = self.base(t.ident.to_string(), "type", &t.attrs);
                o.visibility = Some(visibility(&t.vis));
                Some(o)
            }
            syn::Item::Const(c) => {
                k.visit_item_const(c);
                let mut o = self.base(c.ident.to_string(), "const", &c.attrs);
                o.visibility = Some(visibility(&c.vis));
                Some(o)
            }
            syn::Item::Static(s) => {
                k.visit_item_static(s);
                let mut o = self.base(s.ident.to_string(), "static", &s.attrs);
                o.visibility = Some(visibility(&s.vis));
                Some(o)
            }
            syn::Item::Macro(m) => m.ident.as_ref().map(|id| {
                let mut o = self.base(id.to_string(), "macro", &m.attrs);
                let exported = m.attrs.iter().any(|a| a.path().is_ident("macro_export"));
                o.visibility = Some(if exported { "pub" } else { "private" }.to_string());
                o
            }),
            syn::Item::ForeignMod(fm) => {
                for fi in &fm.items {
                    if let syn::ForeignItem::Fn(f) = fi {
                        let mut fk = Collector::new(&ctx);
                        fk.visit_signature(&f.sig);
                        let mut o = self.base(f.sig.ident.to_string(), "fn", &f.attrs);
                        o.visibility = Some(visibility(&f.vis));
                        (o.refs, o.calls) = fk.finish();
                        out.push(o);
                    }
                }
                None
            }
            _ => None,
        };
        if let Some(mut o) = obj {
            (o.refs, o.calls) = k.finish();
            out.push(o);
        }
        out
    }

    fn impl_block(&self, i: &syn::ItemImpl) -> Vec<Item> {
        let receiver = type_short(&i.self_ty);
        let self_ty = {
            let ctx = Ctx {
                scopes: &self.scopes,
                self_ty: None,
            };
            ctx.type_name(&i.self_ty)
                .unwrap_or_else(|| receiver.clone())
        };
        let ctx = Ctx {
            scopes: &self.scopes,
            self_ty: Some(&self_ty),
        };
        let trait_path = i.trait_.as_ref().map(|(_, p, _)| ctx.resolve(p));
        let name = match &i.trait_ {
            Some((bang, p, _)) => format!(
                "{}{} for {receiver}",
                if bang.is_some() { "!" } else { "" },
                path_short(p)
            ),
            None => receiver.clone(),
        };

        let mut ik = Collector::new(&ctx);
        ik.visit_generics(&i.generics);
        if let Some((_, p, _)) = &i.trait_ {
            ik.visit_path(p);
        }
        ik.visit_type(&i.self_ty);
        let mut methods = Vec::new();
        for ii in &i.items {
            match ii {
                syn::ImplItem::Fn(f) => {
                    let mut fk = Collector::new(&ctx);
                    fk.params(&f.sig);
                    fk.visit_impl_item_fn(f);
                    let mut o = self.base(f.sig.ident.to_string(), "method", &f.attrs);
                    o.test = o.test || is_test(&i.attrs);
                    if i.trait_.is_none() {
                        o.visibility = Some(visibility(&f.vis));
                    }
                    o.receiver = Some(receiver.clone());
                    o.trait_path.clone_from(&trait_path);
                    (o.refs, o.calls) = fk.finish();
                    methods.push(o);
                }
                syn::ImplItem::Const(c) => ik.visit_impl_item_const(c),
                syn::ImplItem::Type(t) => ik.visit_impl_item_type(t),
                _ => {}
            }
        }
        let mut o = self.base(name, "impl", &i.attrs);
        o.receiver = Some(receiver);
        o.trait_path = trait_path;
        (o.refs, o.calls) = ik.finish();
        methods.insert(0, o);
        methods
    }
}

/// Parses `source` (read from `file`) and reports its items.
#[must_use]
pub fn dump_source(file: &str, source: &str) -> FileDump {
    let mut d = FileDump {
        file: file.to_string(),
        ..FileDump::default()
    };
    let ast = match syn::parse_file(source) {
        Ok(ast) => ast,
        Err(e) => {
            let at = e.span().start();
            d.error = Some(format!("{file}:{}:{}: {e}", at.line, at.column + 1));
            return d;
        }
    };
    d.inner_doc = doc_text(&ast.attrs, true);
    for it in &ast.items {
        if let syn::Item::Mod(m) = it {
            d.mods.push(m.ident.to_string());
        }
    }
    let mut w = Dumper {
        test: is_test(&ast.attrs),
        ..Dumper::default()
    };
    w.walk(&ast.items);
    d.items = w.items;
    d
}

/// Reads and dumps one file; a read error is reported in [`FileDump::error`].
#[must_use]
pub fn dump_file(path: &str) -> FileDump {
    match std::fs::read_to_string(path) {
        Ok(src) => dump_source(path, &src),
        Err(e) => FileDump {
            file: path.to_string(),
            error: Some(format!("{path}: {e}")),
            ..FileDump::default()
        },
    }
}

/// The JSON document printed for a set of files: `{"files": [...]}`.
#[derive(Debug, Serialize)]
pub struct Output {
    /// One entry per input path, in argument order.
    pub files: Vec<FileDump>,
}

/// Dumps every path in order.
#[must_use]
pub fn dump_all<I, S>(paths: I) -> Output
where
    I: IntoIterator<Item = S>,
    S: AsRef<str>,
{
    Output {
        files: paths.into_iter().map(|p| dump_file(p.as_ref())).collect(),
    }
}

#[cfg(test)]
mod tests;
