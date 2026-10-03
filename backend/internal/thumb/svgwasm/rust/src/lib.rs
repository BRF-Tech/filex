// filex SVG thumbnail engine: resvg compiled to wasm32-unknown-unknown.
//
// The module has NO imports. It cannot open a file, a socket or a clock:
// the SVG bytes and the font bytes come in through fx_alloc'd memory, the
// premultiplied RGBA pixels go out the same way. Everything else an SVG may
// ask for (an <image> that names a path or a URL, a font that is not one we
// handed over) resolves to nothing.
use std::sync::Arc;

use resvg::tiny_skia;
use resvg::usvg;

struct State {
    fonts: usvg::fontdb::Database,
    out: Vec<u8>,
    out_w: u32,
    out_h: u32,
    err: Vec<u8>,
}

static mut STATE: Option<State> = None;

#[allow(static_mut_refs)]
fn state() -> &'static mut State {
    unsafe {
        if STATE.is_none() {
            STATE = Some(State {
                fonts: usvg::fontdb::Database::new(),
                out: Vec::new(),
                out_w: 0,
                out_h: 0,
                err: Vec::new(),
            });
        }
        STATE.as_mut().unwrap()
    }
}

#[no_mangle]
pub extern "C" fn fx_alloc(len: usize) -> *mut u8 {
    let mut v: Vec<u8> = Vec::with_capacity(len);
    let p = v.as_mut_ptr();
    std::mem::forget(v);
    p
}

#[no_mangle]
pub unsafe extern "C" fn fx_free(ptr: *mut u8, len: usize) {
    if !ptr.is_null() {
        drop(Vec::from_raw_parts(ptr, 0, len));
    }
}

/// Hands one font file (TTF/OTF/TTC) to the engine. The bytes are copied.
#[no_mangle]
pub unsafe extern "C" fn fx_add_font(ptr: *const u8, len: usize) {
    let data = std::slice::from_raw_parts(ptr, len).to_vec();
    state().fonts.load_font_data(data);
}

/// Names the families the generic CSS families fall back to.
#[no_mangle]
pub unsafe extern "C" fn fx_set_families(ptr: *const u8, len: usize) {
    let s = String::from_utf8_lossy(std::slice::from_raw_parts(ptr, len)).to_string();
    let st = state();
    let mut it = s.split('\n');
    if let Some(v) = it.next() { st.fonts.set_sans_serif_family(v); }
    if let Some(v) = it.next() { st.fonts.set_serif_family(v); }
    if let Some(v) = it.next() { st.fonts.set_monospace_family(v); }
}

fn fail(msg: String) -> i32 {
    let st = state();
    st.err = msg.into_bytes();
    st.out = Vec::new();
    1
}

/// Renders the SVG at ptr/len to fit inside max_w x max_h (aspect kept).
/// 0 = ok (fx_out_*), 1 = error (fx_err_*).
#[no_mangle]
pub unsafe extern "C" fn fx_render(ptr: *const u8, len: usize, max_w: u32, max_h: u32) -> i32 {
    let data = std::slice::from_raw_parts(ptr, len);
    let st = state();
    let mut opt = usvg::Options::default();
    opt.resources_dir = None;
    opt.font_family = "Go".to_owned();
    opt.fontdb = Arc::new(st.fonts.clone());
    // Paths and URLs resolve to nothing; embedded data: images still render.
    opt.image_href_resolver = usvg::ImageHrefResolver {
        resolve_data: usvg::ImageHrefResolver::default_data_resolver(),
        resolve_string: Box::new(|_, _| None),
    };
    let tree = match usvg::Tree::from_data(data, &opt) {
        Ok(t) => t,
        Err(e) => return fail(format!("parse: {}", e)),
    };
    let size = tree.size();
    let (w, h) = (size.width() as f64, size.height() as f64);
    if !(w > 0.0 && h > 0.0) || !w.is_finite() || !h.is_finite() {
        return fail("the image has no size".to_owned());
    }
    let s = f64::min(max_w as f64 / w, max_h as f64 / h);
    let ow = ((w * s).round() as u32).max(1);
    let oh = ((h * s).round() as u32).max(1);
    let mut pixmap = match tiny_skia::Pixmap::new(ow, oh) {
        Some(p) => p,
        None => return fail("cannot allocate the canvas".to_owned()),
    };
    resvg::render(&tree, tiny_skia::Transform::from_scale(s as f32, s as f32), &mut pixmap.as_mut());
    st.out = pixmap.take();
    st.out_w = ow;
    st.out_h = oh;
    st.err = Vec::new();
    0
}

#[no_mangle]
pub extern "C" fn fx_out_ptr() -> *const u8 { state().out.as_ptr() }
#[no_mangle]
pub extern "C" fn fx_out_len() -> usize { state().out.len() }
#[no_mangle]
pub extern "C" fn fx_out_w() -> u32 { state().out_w }
#[no_mangle]
pub extern "C" fn fx_out_h() -> u32 { state().out_h }
#[no_mangle]
pub extern "C" fn fx_err_ptr() -> *const u8 { state().err.as_ptr() }
#[no_mangle]
pub extern "C" fn fx_err_len() -> usize { state().err.len() }
