// Trace Buddy en 3D: un pequeño hacker de QA con capucha, construido por código con three.js (sin modelos
// externos). La cara vive en el visor (un canvas que se redibuja: pestañea, sigue al puntero, habla y cambia
// de expresión), el cuerpo hace gestos según lo que pasa (asiente, pulgar arriba, mano al visor, se rasca la
// cabeza, se duerme si nadie lo mira) y la ropa cambia con el tema de TraceReports.
// Lo carga app.js con import() solo cuando Trace Buddy está visible; si no hay WebGL, queda la imagen.
import * as THREE from "./three.module.min.js";

// La ropa de cada tema: sudadera, detalles de neón (dos tonos), ojos del visor y suelas.
const OUTFITS = {
	"trace": { hoodie: 0x2a3140, inner: 0x161b25, trimA: 0xc9fa6b, trimB: 0x29dfff, eyes: "#7dfcff", sole: 0xc9fa6b },
	"trace-dark": { hoodie: 0x171a26, inner: 0x0c0e16, trimA: 0x29dfff, trimB: 0x8656ff, eyes: "#5ff0ff", sole: 0x29dfff },
	"midnight": { hoodie: 0x1b1840, inner: 0x0d0b24, trimA: 0xa78bfa, trimB: 0x6d5dfc, eyes: "#c4b5fd", sole: 0xa78bfa },
	"paper": { hoodie: 0xd9cdb4, inner: 0x6b5a45, trimA: 0xb4532a, trimB: 0x3f6f73, eyes: "#7ee0d6", sole: 0xb4532a },
	"pixel": { hoodie: 0x2b2f6b, inner: 0x161838, trimA: 0xff5fa2, trimB: 0xffd23f, eyes: "#ffe66d", sole: 0xff5fa2, flat: true },
	"terminal": { hoodie: 0x0e1410, inner: 0x060a07, trimA: 0x39ff88, trimB: 0x1fb85f, eyes: "#5dff9c", sole: 0x39ff88 },
};

// Las caras: forma de los ojos y de la boca sobre el visor (canvas de 512x256).
const FACES = {
	normal: { eyes: "round", mouth: "smile" },
	happy: { eyes: "arc", mouth: "grin" },
	laugh: { eyes: "arc", mouth: "open" },
	focused: { eyes: "narrow", mouth: "flat" },
	worried: { eyes: "worried", mouth: "frown" },
	surprised: { eyes: "o", mouth: "o" },
	wink: { eyes: "wink", mouth: "smile" },
	curious: { eyes: "curious", mouth: "side" },
	sleepy: { eyes: "closed", mouth: "flat" },
	stars: { eyes: "star", mouth: "grin" },
	love: { eyes: "heart", mouth: "grin" },
	shy: { eyes: "shy", mouth: "wavy", blush: true },
	shush: { eyes: "wink", mouth: "o" }, // "shhh": un guiño y la boquita en o
	proud: { eyes: "arc", mouth: "side" }, // seguro de sí mismo
};
const MOOD_FACE = { idle: "normal", thinking: "focused", success: "stars", error: "worried" };

// Gestos: cuánto duran (s). Cada uno mueve brazos, cabeza y cuerpo en step().
const GESTURES = { wave: 2.2, nod: 0.9, thumbs: 1.8, facepalm: 1.8, scratch: 2.4, chin: 2.4, jump: 1.0, spinJoy: 1.2, stretch: 2.2, lookAround: 3.0, yawn: 2.0,
	shush: 1.9, proud: 2.2 };

/** Monta el personaje en container (un elemento con tamaño). Devuelve sus controles, o null sin WebGL. */
export function mountBuddy3D(container, { theme = "trace-dark", reducedMotion = false, onEvent = () => {} } = {}) {
	let renderer;
	try {
		renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: "low-power" });
	} catch {
		return null; // sin WebGL: queda la imagen
	}
	const size = () => [container.clientWidth || 120, container.clientHeight || 120];
	const basePR = Math.min(window.devicePixelRatio || 1, 2);
	renderer.setPixelRatio(basePR);
	renderer.outputColorSpace = THREE.SRGBColorSpace;
	renderer.toneMapping = THREE.ACESFilmicToneMapping;
	renderer.setSize(...size());
	renderer.domElement.className = "buddy3d-canvas";
	renderer.domElement.setAttribute("aria-hidden", "true");
	container.appendChild(renderer.domElement);

	const scene = new THREE.Scene();
	const camera = new THREE.PerspectiveCamera(28, 1, 0.1, 50);
	camera.position.set(0, 0.55, 7.4);
	camera.lookAt(0, 0.05, 0);

	// luz: suave desde arriba, una principal y dos de contorno de neón (como el concepto)
	scene.add(new THREE.HemisphereLight(0xbcd8ff, 0x1a1030, 1.15));
	const key = new THREE.DirectionalLight(0xffffff, 1.6);
	key.position.set(2.5, 4, 5);
	scene.add(key);
	const rimA = new THREE.PointLight(0x29dfff, 14, 12);
	rimA.position.set(-2.6, 1.2, -1.2);
	const rimB = new THREE.PointLight(0x8656ff, 14, 12);
	rimB.position.set(2.6, 0.4, -1.4);
	scene.add(rimA, rimB);

	// ---------- materiales (se recolorean al cambiar de tema) ----------
	const mHoodie = new THREE.MeshStandardMaterial({ roughness: 0.82, metalness: 0.0 });
	const mInner = new THREE.MeshStandardMaterial({ roughness: 0.95, side: THREE.DoubleSide });
	const mTrimA = new THREE.MeshStandardMaterial({ roughness: 0.4, emissiveIntensity: 2.2 });
	const mTrimB = new THREE.MeshStandardMaterial({ roughness: 0.4, emissiveIntensity: 2.2 });
	const mGlove = new THREE.MeshStandardMaterial({ color: 0x15161d, roughness: 0.55 });
	const mMetal = new THREE.MeshStandardMaterial({ color: 0xaab4c8, roughness: 0.25, metalness: 0.9 });
	const mVisor = new THREE.MeshPhysicalMaterial({ color: 0x05060a, roughness: 0.08, metalness: 0.2, clearcoat: 1, clearcoatRoughness: 0.05 });
	const mSole = new THREE.MeshStandardMaterial({ roughness: 0.5, emissiveIntensity: 1.6 });

	const buddy = new THREE.Group();
	scene.add(buddy);
	const body = new THREE.Group(); // lo que respira y se inclina
	buddy.add(body);

	// ---------- cuerpo: sudadera (torno), bolsillo, cordones y </> ----------
	const torsoProfile = [[0, -0.95], [0.62, -0.95], [0.7, -0.75], [0.68, -0.3], [0.6, 0.05], [0.45, 0.28], [0.2, 0.36], [0, 0.37]]
		.map(([x, y]) => new THREE.Vector2(x, y));
	const torso = new THREE.Mesh(new THREE.LatheGeometry(torsoProfile, 40), mHoodie);
	torso.position.y = -0.55;
	body.add(torso);
	const hem = new THREE.Mesh(new THREE.TorusGeometry(0.64, 0.045, 10, 48), mTrimB);
	hem.rotation.x = Math.PI / 2;
	hem.position.y = -1.48;
	body.add(hem);
	// el bolsillo canguro: un trozo de cilindro apenas por fuera de la sudadera (en el cilindro, θ = 0 es el frente)
	const pocket = new THREE.Mesh(new THREE.CylinderGeometry(0.715, 0.705, 0.3, 24, 1, true, -0.5, 1.0), mInner);
	pocket.position.y = -1.0;
	body.add(pocket);
	for (const x of [-0.13, 0.13]) {
		const cord = new THREE.Mesh(new THREE.CylinderGeometry(0.018, 0.018, 0.36, 8), mGlove);
		cord.position.set(x, -0.38, 0.6);
		cord.rotation.x = -0.25;
		const tip = new THREE.Mesh(new THREE.CylinderGeometry(0.032, 0.032, 0.09, 12), mMetal);
		tip.position.set(x, -0.58, 0.65);
		body.add(cord, tip);
	}
	const codeTex = textTexture("</>", 128, 64);
	const code = new THREE.Mesh(new THREE.PlaneGeometry(0.42, 0.21),
		new THREE.MeshBasicMaterial({ map: codeTex, transparent: true, toneMapped: false, depthWrite: false }));
	code.position.set(0, -0.68, 0.672);
	code.rotation.x = -0.12;
	body.add(code);

	// ---------- piernas y botas con suela de neón ----------
	const feet = [];
	for (const x of [-0.27, 0.27]) {
		const foot = new THREE.Group();
		const leg = new THREE.Mesh(new THREE.CapsuleGeometry(0.17, 0.22, 6, 16), mInner);
		leg.position.set(x, -1.62, 0.02);
		const boot = new THREE.Mesh(new THREE.CapsuleGeometry(0.2, 0.2, 8, 20), mGlove);
		boot.rotation.x = Math.PI / 2;
		boot.scale.set(1.05, 1, 0.75);
		boot.position.set(x, -1.86, 0.1);
		const sole = new THREE.Mesh(new THREE.TorusGeometry(0.2, 0.03, 8, 30), mSole);
		sole.rotation.x = Math.PI / 2;
		sole.scale.set(1.1, 1.55, 1);
		sole.position.set(x, -1.98, 0.1);
		foot.add(leg, boot, sole);
		body.add(foot);
		feet.push(foot);
	}

	// ---------- brazos: hombro → mano, con puño de neón; la mano derecha tiene pulgar ----------
	const arm = (side) => {
		const pivot = new THREE.Group();
		pivot.position.set(side * 0.6, -0.42, 0);
		const sleeve = new THREE.Mesh(new THREE.CapsuleGeometry(0.16, 0.42, 6, 16), mHoodie);
		sleeve.position.y = -0.3;
		const cuff = new THREE.Mesh(new THREE.TorusGeometry(0.15, 0.035, 8, 24), mTrimB);
		cuff.rotation.x = Math.PI / 2;
		cuff.position.y = -0.58;
		const hand = new THREE.Mesh(new THREE.SphereGeometry(0.17, 20, 16), mGlove);
		hand.scale.set(1, 1.1, 0.9);
		hand.position.y = -0.72;
		pivot.add(sleeve, cuff, hand);
		pivot.rotation.z = side * 0.32;
		body.add(pivot);
		return pivot;
	};
	const armL = arm(-1), armR = arm(1);
	const thumb = new THREE.Mesh(new THREE.CapsuleGeometry(0.06, 0.12, 6, 12), mGlove);
	thumb.position.set(0, -0.62, 0.16);
	thumb.rotation.x = -0.4;
	thumb.scale.setScalar(0.001); // aparece solo con el pulgar arriba
	armR.add(thumb);

	// ---------- cabeza: capucha con punta, borde de neón y visor con la cara ----------
	const head = new THREE.Group();
	head.position.y = 0.55;
	body.add(head);
	// la capucha: una esfera con un hueco redondo al frente (el polo de la esfera, girado hacia +Z); el visor
	// queda hundido adentro, como en el concepto, y no se sale al girar la cabeza
	const HOLE = 0.72; // medio ángulo del hueco (rad)
	const hoodGeo = new THREE.SphereGeometry(1.0, 56, 40, 0, Math.PI * 2, HOLE, Math.PI - HOLE);
	hoodGeo.rotateX(Math.PI / 2);
	const hood = new THREE.Mesh(hoodGeo, mHoodie);
	hood.material.side = THREE.DoubleSide;
	hood.scale.set(1.2, 1.12, 1.08);
	const hoodGroup = new THREE.Group(); // todo lo que es capucha: se saca y se pone al cambiarse de ropa
	head.add(hoodGroup);
	hoodGroup.add(hood);
	// el fondo oscuro de la capucha, detrás del visor
	const hoodInner = new THREE.Mesh(new THREE.SphereGeometry(0.95, 40, 30), mInner);
	hoodInner.position.z = -0.34;
	hoodGroup.add(hoodInner);
	// la punta de la capucha, caída hacia atrás
	const hoodTip = new THREE.Mesh(new THREE.ConeGeometry(0.42, 0.95, 28), mHoodie);
	hoodTip.position.set(0, 1.02, -0.32);
	hoodTip.rotation.x = -0.75;
	hoodGroup.add(hoodTip);
	// el borde del hueco: dos medios aros de neón justo en el borde de la capucha
	const rimR = Math.sin(HOLE), rimZ = Math.cos(HOLE) * 1.08;
	const rimTop = new THREE.Mesh(new THREE.TorusGeometry(rimR, 0.028, 10, 64, Math.PI), mTrimA);
	const rimBot = new THREE.Mesh(new THREE.TorusGeometry(rimR, 0.028, 10, 64, Math.PI), mTrimB);
	rimBot.rotation.z = Math.PI;
	for (const r of [rimTop, rimBot]) {
		r.scale.set(1.2, 1.12, 1);
		r.position.z = rimZ;
	}
	hoodGroup.add(rimTop, rimBot);
	// el visor: negro brillante; la cara es un parche de esfera un poco por delante
	const visor = new THREE.Mesh(new THREE.SphereGeometry(0.8, 48, 36), mVisor);
	visor.scale.set(1.12, 0.92, 0.82);
	visor.position.z = 0.1;
	head.add(visor);
	const faceCanvas = document.createElement("canvas");
	faceCanvas.width = 512;
	faceCanvas.height = 256;
	const faceCtx = faceCanvas.getContext("2d");
	const faceTex = new THREE.CanvasTexture(faceCanvas);
	faceTex.colorSpace = THREE.SRGBColorSpace;
	const face = new THREE.Mesh(
		new THREE.SphereGeometry(0.803, 48, 24, Math.PI * 0.18, Math.PI * 0.64, Math.PI * 0.3, Math.PI * 0.42),
		new THREE.MeshBasicMaterial({ map: faceTex, transparent: true, toneMapped: false, depthWrite: false, blending: THREE.AdditiveBlending }));
	face.scale.copy(visor.scale);
	face.position.copy(visor.position);
	head.add(face);
	// el símbolo de la capucha
	const hoodMark = new THREE.Mesh(new THREE.PlaneGeometry(0.34, 0.17),
		new THREE.MeshBasicMaterial({ map: textTexture("</>", 128, 64), transparent: true, toneMapped: false, depthWrite: false }));
	hoodMark.position.set(0, 0.93, 0.6);
	hoodMark.rotation.x = -0.6;
	hoodGroup.add(hoodMark);

	// ---------- partículas: datos que orbitan al analizar, chispas al cambiar de ropa, zzz al dormir ----------
	const sparkGeo = new THREE.BufferGeometry();
	const SPARKS = 60;
	const sparkPos = new Float32Array(SPARKS * 3);
	sparkGeo.setAttribute("position", new THREE.BufferAttribute(sparkPos, 3));
	const sparkMat = new THREE.PointsMaterial({ size: 0.09, transparent: true, opacity: 0, blending: THREE.AdditiveBlending, depthWrite: false, toneMapped: false });
	const sparks = new THREE.Points(sparkGeo, sparkMat);
	scene.add(sparks);
	const sparkVel = Array.from({ length: SPARKS }, () => new THREE.Vector3());
	const zzz = new THREE.Mesh(new THREE.PlaneGeometry(0.5, 0.5),
		new THREE.MeshBasicMaterial({ map: textTexture("z", 64, 64), transparent: true, opacity: 0, toneMapped: false, depthWrite: false }));
	scene.add(zzz);

	// ---------- estado ----------
	const st = {
		mood: "idle", face: "normal", faceUntil: 0, outfit: OUTFITS[theme] || OUTFITS["trace-dark"],
		blink: 0, nextBlink: 1.5, look: new THREE.Vector2(), lookTarget: new THREE.Vector2(), near: 0,
		gesture: null, gT: 0, gDur: 0, hop: 0, shake: 0, sparkLife: 0, hover: 0,
		talk: 0, idleFor: 0, asleep: false, panel: false, nextFidget: 9,
		hoverFor: 0, blush: 0, change: null, flying: [],
		running: false, visible: true, last: 0, t: 0,
	};
	const ease = (k) => k * k * (3 - 2 * k);
	const bell = (k) => Math.sin(Math.min(1, Math.max(0, k)) * Math.PI); // 0 → 1 → 0

	function gesture(name) {
		if (reducedMotion) return;
		st.gesture = name;
		st.gT = 0;
		st.gDur = GESTURES[name] || 1;
		if (name === "jump" || name === "spinJoy") st.hop = 1;
	}
	function show(faceName, secs) { st.face = faceName; st.faceUntil = st.t + secs; }

	function applyOutfit(o) {
		st.eyes = o.eyes; // los ojos cambian con la ropa (en el cambio, al final, no al empezar)
		mHoodie.color.setHex(o.hoodie);
		mInner.color.setHex(o.inner);
		for (const [m, c] of [[mTrimA, o.trimA], [mTrimB, o.trimB], [mSole, o.sole]]) {
			m.color.setHex(c);
			m.emissive.setHex(c);
		}
		for (const m of [mHoodie, mInner, mGlove]) {
			m.flatShading = !!o.flat; // Pixel: aspecto de polígonos, como un juego retro
			m.needsUpdate = true;
		}
		rimA.color.setHex(o.trimA);
		rimB.color.setHex(o.trimB);
		recolorText(codeTex, o.trimA);
		recolorText(zzz.material.map, o.trimA);
		sparkMat.color.setHex(o.trimA);
	}

	// ---------- la cara: ojos y boca de luz sobre el visor ----------
	function drawFace() {
		const o = { eyes: st.eyes }, f = FACES[st.face] || FACES.normal;
		const ctx = faceCtx;
		ctx.clearRect(0, 0, 512, 256);
		ctx.save();
		ctx.translate(st.look.x * 44, st.look.y * 22); // los ojos siguen al puntero
		ctx.fillStyle = o.eyes;
		ctx.strokeStyle = o.eyes;
		ctx.shadowColor = o.eyes;
		ctx.shadowBlur = 26;
		ctx.lineCap = "round";
		ctx.lineJoin = "round";
		const open = Math.max(0.08, 1 - st.blink); // 1 = abierto
		for (const side of [-1, 1]) {
			const x = 256 + side * 92, y = 108;
			ctx.lineWidth = 18;
			switch (f.eyes) {
			case "arc":
				ctx.lineWidth = 20; ctx.beginPath(); ctx.arc(x, y + 18, 40, Math.PI * 1.1, Math.PI * 1.9); ctx.stroke(); break;
			case "narrow":
				roundRect(ctx, x - 36, y - 8 * open, 72, 24 * open, 12); ctx.fill(); break;
			case "worried":
				ctx.save(); ctx.translate(x, y); ctx.rotate(side * -0.3);
				roundRect(ctx, -28, -30 * open, 56, 56 * open, 24); ctx.fill(); ctx.restore(); break;
			case "o":
				ctx.lineWidth = 15; ctx.beginPath(); ctx.ellipse(x, y, 32, 40 * open, 0, 0, Math.PI * 2); ctx.stroke(); break;
			case "wink":
				if (side === 1) { ctx.lineWidth = 20; ctx.beginPath(); ctx.arc(x, y + 18, 36, Math.PI * 1.1, Math.PI * 1.9); ctx.stroke(); break; }
				roundRect(ctx, x - 34, y - 46 * open, 68, 92 * open, 32); ctx.fill(); break;
			case "curious": { // un ojo más grande que el otro
				const k = side === -1 ? 1.15 : 0.8;
				roundRect(ctx, x - 34 * k, y - 46 * k * open, 68 * k, 92 * k * open, 32 * k); ctx.fill(); break;
			}
			case "closed":
				ctx.lineWidth = 14; ctx.beginPath(); ctx.arc(x, y - 4, 34, Math.PI * 0.15, Math.PI * 0.85); ctx.stroke(); break;
			case "star":
				star(ctx, x, y, 46, 20); ctx.fill(); break;
			case "heart":
				heart(ctx, x, y, 44); ctx.fill(); break;
			case "shy": // ojitos que miran hacia abajo, de lado
				ctx.lineWidth = 15; ctx.beginPath(); ctx.arc(x - 10, y + 22, 26, Math.PI * 1.15, Math.PI * 1.85); ctx.stroke(); break;
			default:
				roundRect(ctx, x - 34, y - 46 * open, 68, 92 * open, 32); ctx.fill();
			}
		}
		// la boca: al hablar se abre y se cierra
		ctx.lineWidth = 12;
		ctx.beginPath();
		if (st.talk > 0) {
			const m = 6 + Math.abs(Math.sin(st.t * 16)) * 20;
			ctx.ellipse(256, 180, 26, m, 0, 0, Math.PI * 2); ctx.fill();
		} else {
			switch (f.mouth) {
			case "grin": ctx.arc(256, 168, 40, Math.PI * 0.12, Math.PI * 0.88); ctx.stroke(); break;
			case "open": ctx.arc(256, 166, 38, 0, Math.PI); ctx.closePath(); ctx.fill(); break;
			case "flat": ctx.moveTo(232, 184); ctx.lineTo(280, 184); ctx.stroke(); break;
			case "frown": ctx.arc(256, 206, 26, Math.PI * 1.15, Math.PI * 1.85); ctx.stroke(); break;
			case "o": ctx.ellipse(256, 186, 14, 18, 0, 0, Math.PI * 2); ctx.stroke(); break;
			case "side": ctx.moveTo(240, 186); ctx.quadraticCurveTo(262, 192, 284, 176); ctx.stroke(); break;
			case "wavy": // boquita nerviosa
				ctx.lineWidth = 9; ctx.moveTo(226, 184);
				for (let i = 1; i <= 4; i++) ctx.quadraticCurveTo(226 + i * 12 - 6, 184 + (i % 2 ? -8 : 8), 226 + i * 12, 184);
				ctx.stroke(); break;
			default: ctx.arc(256, 160, 30, Math.PI * 0.2, Math.PI * 0.8); ctx.stroke();
			}
		}
		// mejillas: más encendidas cuando está contento; rojas de vergüenza, con rayitas, cuando lo miran mucho
		ctx.shadowBlur = 0;
		if (f.blush || st.blush > 0.05) {
			const k = Math.max(st.blush, f.blush ? 1 : 0);
			ctx.globalAlpha = 0.9 * k;
			ctx.fillStyle = "#ff3d6e";
			ctx.shadowColor = "#ff3d6e";
			ctx.shadowBlur = 30;
			for (const side of [-1, 1]) { ctx.beginPath(); ctx.ellipse(256 + side * 140, 166, 46, 22, 0, 0, Math.PI * 2); ctx.fill(); }
			ctx.strokeStyle = "#ffd1dc";
			ctx.lineWidth = 6;
			ctx.shadowBlur = 0;
			for (const side of [-1, 1]) for (let i = -1; i <= 1; i++) {
				const cx = 256 + side * 140 + i * 16;
				ctx.beginPath(); ctx.moveTo(cx - 6, 176); ctx.lineTo(cx + 6, 156); ctx.stroke();
			}
		} else {
			ctx.globalAlpha = f.eyes === "heart" || f.eyes === "star" || f.mouth === "open" ? 0.5 : 0.25;
			for (const side of [-1, 1]) { ctx.beginPath(); ctx.ellipse(256 + side * 150, 164, 26, 11, 0, 0, Math.PI * 2); ctx.fill(); }
		}
		ctx.restore();
		faceTex.needsUpdate = true;
	}

	function burst(count, speed) {
		st.sparkLife = 1;
		for (let i = 0; i < SPARKS; i++) {
			const a = Math.random() * Math.PI * 2, b = Math.random() * Math.PI - Math.PI / 2;
			sparkPos[i * 3] = 0; sparkPos[i * 3 + 1] = 0.2; sparkPos[i * 3 + 2] = 0;
			const v = i < count ? speed * (0.6 + Math.random() * 0.6) : 0;
			sparkVel[i].set(Math.cos(a) * Math.cos(b) * v, Math.sin(b) * v + 0.6 * v, Math.sin(a) * Math.cos(b) * v);
		}
		sparkGeo.attributes.position.needsUpdate = true;
	}

	// ---------- animación ----------
	function frame(now) {
		if (!st.running) return;
		requestAnimationFrame(frame);
		if (st.last && now - st.last < 30) return; // ~30 fps: suficiente y más liviano
		const dt = Math.min(0.05, st.last ? (now - st.last) / 1000 : 0.016);
		st.last = now;
		st.t += dt;
		step(dt);
		renderer.render(scene, camera);
	}

	const lerp = (a, b, k) => a + (b - a) * k;
	function step(dt) {
		const t = st.t, k = Math.min(1, dt * 6);
		// aburrimiento: sin puntero mucho rato mira a los lados, bosteza y se duerme
		st.idleFor += dt;
		if (!st.asleep && st.mood === "idle" && !st.gesture) {
			st.nextFidget -= dt;
			if (st.idleFor > 40) { st.asleep = true; gesture("yawn"); }
			else if (st.nextFidget <= 0) {
				st.nextFidget = 7 + Math.random() * 7;
				const pick = ["lookAround", "stretch", "wink", "curious"][Math.floor(Math.random() * 4)];
				if (pick === "wink") show("wink", 0.7); else if (pick === "curious") show("curious", 1.6); else gesture(pick);
			}
		}
		// pestañeo
		st.nextBlink -= dt;
		if (st.nextBlink <= 0) { st.blink = 1; st.nextBlink = 2.2 + Math.random() * 3; }
		st.blink = Math.max(0, st.blink - dt * 7);
		if (st.talk > 0) st.talk = Math.max(0, st.talk - dt);
		// mirada: al puntero; con el panel abierto, hacia la conversación; al analizar, de lado a lado
		const target = st.lookTarget.clone();
		if (st.mood === "thinking") target.set(Math.sin(t * 2.2) * 0.9, -0.45);
		else if (st.panel && st.idleFor > 1.5) target.set(-0.75, -0.55);
		if (st.gesture === "lookAround") target.set(Math.sin((st.gT / st.gDur) * Math.PI * 2) * 1, -0.1);
		if (st.asleep) target.set(0, 0.5);
		if (st.hoverFor > 2.2 && !st.change) target.set(-0.35, 0.45); // tímido: mira abajo, de lado
		if (st.change && st.change.t < 0.7) target.set(Math.sin(st.change.t * 9) * 1, 0); // ¿alguien mira?
		st.look.lerp(target, k);
		// vergüenza: con el cursor encima mucho rato se sonroja, mira para otro lado y junta las manos
		st.hoverFor = st.hover ? st.hoverFor + dt : 0;
		const shy = st.hoverFor > 2.2 && !st.change && !st.asleep;
		if (shy && !st.wasShy) onEvent("shy");
		st.wasShy = shy;
		st.blush = lerp(st.blush, shy ? 1 : 0, Math.min(1, dt * 3));
		mVisor.emissive.setRGB(0.12 * st.blush, 0.0, 0.03 * st.blush); // el visor se pone apenas rojizo
		// cara: la del momento (gesto o reacción); tímido; dormido; si no, la del ánimo
		st.face = st.faceUntil > t ? st.face : shy ? "shy" : st.asleep ? "sleepy" : MOOD_FACE[st.mood] || "normal";
		drawFace();

		// gesto en curso: avance 0..1
		let g = null, p = 0;
		if (st.gesture) {
			st.gT += dt;
			p = st.gT / st.gDur;
			if (p >= 1) st.gesture = null; else g = st.gesture;
		}
		const b = bell(p);

		// cuerpo: flota y respira (dormido, lento); se inclina hacia el puntero cuando está cerca
		const breath = st.asleep ? 0.8 : 1.7;
		const bob = Math.sin(t * breath) * 0.06;
		const hopY = st.hop > 0 ? Math.sin((1 - st.hop) * Math.PI) * 0.5 : 0;
		buddy.position.y = bob + hopY;
		if (st.hop > 0) st.hop = Math.max(0, st.hop - dt * 1.8);
		const squash = st.hop > 0.85 || (st.hop > 0 && st.hop < 0.12) ? 0.06 : 0;
		body.scale.set(1 + Math.sin(t * breath + 1) * 0.012 + squash, 1 - Math.sin(t * breath + 1) * 0.012 - squash, 1);
		const lean = st.near * 0.12;
		body.rotation.z = lerp(body.rotation.z, -st.look.x * lean + (g === "stretch" ? Math.sin(p * Math.PI * 2) * 0.12 : 0), k);
		body.rotation.x = lerp(body.rotation.x, st.near * 0.08 + (st.asleep ? 0.08 : 0), k);

		// cabeza: hacia donde mira; asiente; se ladea con curiosidad; cae al dormir
		let hx = -st.look.y * 0.3, hy = st.look.x * 0.42, hz = Math.sin(t * 0.9) * 0.035;
		if (g === "nod") hx += Math.sin(p * Math.PI * 4) * 0.22 * (1 - p);
		if (st.face === "curious") hz += 0.22;
		if (g === "yawn") { hx -= b * 0.35; }
		if (st.asleep) { hx = 0.32 + Math.sin(t * 0.8) * 0.03; hy *= 0.2; hz = 0.15; }
		if (st.blush > 0.3) { hx += 0.16 * st.blush; hy -= 0.18 * st.blush; hz -= 0.1 * st.blush; } // agacha la cara, de lado
		if (st.mood === "error") hz += Math.sin(t * 30) * 0.03 * st.shake;
		head.rotation.x = lerp(head.rotation.x, hx, k);
		head.rotation.y = lerp(head.rotation.y, hy, k);
		head.rotation.z = lerp(head.rotation.z, hz, k);

		// todo el cuerpo gira un poco hacia el puntero
		let turn = st.look.x * 0.3 + st.hover * 0.12;
		if (g === "spinJoy") turn += ease(p) * Math.PI * 2;
		buddy.rotation.y = lerp(buddy.rotation.y, turn, g === "spinJoy" ? 1 : k);
		if (st.shake > 0) { st.shake = Math.max(0, st.shake - dt * 1.2); buddy.position.x = Math.sin(t * 40) * 0.05 * st.shake; } else buddy.position.x = 0;

		// brazos: cada gesto tiene su pose; sin gesto, la del ánimo
		let rz = 0.32 + Math.sin(t * breath) * 0.04, rx = 0, lz = -0.32 - Math.sin(t * breath) * 0.04, lx = 0, thumbOn = 0;
		if (st.blush > 0.3) { // brazos cruzados sobre el pecho, y se balancea
			rz = -0.62; rx = -1.42; lz = 0.62; lx = -1.22;
			body.rotation.z += Math.sin(t * 3) * 0.04 * st.blush;
		}
		switch (g) {
		case "wave": rz = 2.4 + Math.sin(t * 14) * 0.35; rx = -0.2; break;
		case "thumbs": rz = 0.32 + b * 1.6; rx = -b * 1.1; thumbOn = b; break;
		case "facepalm": rz = 0.32 + b * 2.25; rx = -b * 1.35; break;
		case "shush": rz = 0.32 + b * 1.85; rx = -b * 1.5; break; // la mano a la "boca" del visor
		case "proud": rz = -0.62; rx = -1.42; lz = 0.62; lx = -1.22; break; // brazos cruzados
		case "scratch": rz = 2.7; rx = -0.5 + Math.sin(t * 18) * 0.12; break;
		case "chin": rz = 1.9; rx = -1.0; break;
		case "stretch": rz = 0.32 + b * 2.6; lz = -0.32 - b * 2.6; break;
		case "yawn": lz = -0.32 - b * 2.0; lx = -b * 1.0; break;
		case "jump": case "spinJoy": rz = 0.32 + b * 2.4; lz = -0.32 - b * 2.4; break;
		default:
			if (st.mood === "thinking") { rz = 1.9; rx = -0.95; } // mano al mentón
			if (st.mood === "success") { rz = 0.6; }
		}
		armR.rotation.z = lerp(armR.rotation.z, rz, k);
		armR.rotation.x = lerp(armR.rotation.x, rx, k);
		armL.rotation.z = lerp(armL.rotation.z, lz, k);
		armL.rotation.x = lerp(armL.rotation.x, lx, k);
		thumb.scale.setScalar(Math.max(0.001, lerp(thumb.scale.x, thumbOn, k)));
		// pies: un paso al girar o celebrar
		feet[0].position.y = g === "jump" || g === "spinJoy" ? -b * 0.08 : 0;
		feet[1].position.y = g === "jump" || g === "spinJoy" ? -b * 0.08 : 0;

		changeStep(dt);
		flyStep(dt);

		// partículas: datos al analizar; chispas al vestirse o celebrar; zzz al dormir
		if (st.mood === "thinking") {
			sparkMat.opacity = 0.9;
			for (let i = 0; i < 14; i++) {
				const a = t * 1.6 + (i / 14) * Math.PI * 2;
				sparkPos[i * 3] = Math.cos(a) * 1.3; sparkPos[i * 3 + 1] = 0.6 + Math.sin(a * 2) * 0.15; sparkPos[i * 3 + 2] = Math.sin(a) * 1.3;
			}
			for (let i = 14; i < SPARKS; i++) sparkPos[i * 3 + 1] = -99;
			sparkGeo.attributes.position.needsUpdate = true;
		} else if (st.sparkLife > 0) {
			st.sparkLife = Math.max(0, st.sparkLife - dt * 1.1);
			sparkMat.opacity = st.sparkLife;
			for (let i = 0; i < SPARKS; i++) {
				sparkPos[i * 3] += sparkVel[i].x * dt; sparkPos[i * 3 + 1] += sparkVel[i].y * dt; sparkPos[i * 3 + 2] += sparkVel[i].z * dt;
				sparkVel[i].y -= dt * 2.5;
			}
			sparkGeo.attributes.position.needsUpdate = true;
		} else sparkMat.opacity = 0;
		const zc = (t * 0.45) % 1;
		zzz.material.opacity = st.asleep ? bell(zc) : 0;
		zzz.position.set(0.9 + zc * 0.4, 1.3 + zc * 0.9, 0.3);
		zzz.scale.setScalar(0.5 + zc * 0.6);
	}

	// ---------- el cambio de ropa, paso a paso (≈3,4 s) ----------
	// 0–0,7 mira a los lados · 0,7 se saca la capucha (sale volando) · 0,9–2,3 censura: se retuerce, vuela la ropa
	// vieja y aparece la nueva pieza por pieza · 2,3 la capucha nueva cae desde arriba · 2,9 se arregla, orgulloso
	const CHANGE = 3.4;
	function changeStep(dt) {
		const c = st.change;
		if (!c) return;
		const prev = c.t;
		c.t += dt;
		const at = (s) => prev < s && c.t >= s; // pasó por el segundo s en este cuadro
		const between = (a, b) => c.t >= a && c.t < b;
		if (at(0.7)) { // se saca la capucha: la vieja sale volando con su color de antes
			throwPiece(hoodGroup, new THREE.Vector3(0.4, 3.2, -0.6), new THREE.Vector3(1.5, 0.6, 2.4));
			hoodGroup.visible = false;
		}
		if (at(0.9)) censor(true);
		if (censored) censorBlocks(c.t);
		if (between(0.9, 2.3)) { // se retuerce detrás de la censura
			buddy.rotation.y += Math.sin(c.t * 11) * 0.5;
			armR.rotation.z = 1.6 + Math.sin(c.t * 15) * 0.9;
			armL.rotation.z = -1.6 - Math.cos(c.t * 13) * 0.9;
		}
		if (at(1.1)) { throwPiece(torso, new THREE.Vector3(-2.6, 1.6, 0.8), new THREE.Vector3(0.5, 2.5, 1), 0.35); mHoodie.color.setHex(c.to.hoodie); mInner.color.setHex(c.to.inner); }
		if (at(1.4)) { tint(mTrimB, c.to.trimB); burst(18, 1.4); } // mangas y ruedo
		if (at(1.7)) { tint(mSole, c.to.sole); burst(12, 1.0); } // botas
		if (at(1.95)) { tint(mTrimA, c.to.trimA); applyOutfit(c.to); } // detalles y luces
		if (at(2.3)) { hoodGroup.visible = true; censor(false); }
		if (c.t >= 2.3 && c.t < 2.9) { // la capucha nueva cae y rebota
			const q = (c.t - 2.3) / 0.6;
			hoodGroup.position.y = (1 - q) * 1.6 + Math.sin(q * Math.PI) * 0.12;
			if (at(2.85)) { burst(SPARKS, 2.4); show("stars", 1.0); }
		} else hoodGroup.position.y = 0;
		if (between(2.9, CHANGE)) { // se arregla la ropa con las manos
			armR.rotation.z = 0.7 + Math.sin(c.t * 18) * 0.15;
			armL.rotation.z = -0.7 - Math.sin(c.t * 18) * 0.15;
		}
		if (at(3.15)) show("wink", 0.6);
		if (c.t >= CHANGE) { st.change = null; hoodGroup.position.y = 0; hoodGroup.visible = true; onEvent("changeEnd"); }
	}
	function tint(m, hex) { m.color.setHex(hex); m.emissive?.setHex(hex); }

	// una pieza de ropa que sale volando (una copia con su color de antes), girando y desvaneciéndose
	function throwPiece(obj, vel, spin, scale = 1) {
		const piece = obj.clone(true);
		piece.traverse((m) => {
			if (!m.material) return;
			m.material = m.material.clone();
			m.material.transparent = true;
		});
		obj.updateWorldMatrix(true, false);
		obj.matrixWorld.decompose(piece.position, piece.quaternion, piece.scale);
		piece.scale.multiplyScalar(scale);
		scene.add(piece);
		st.flying.push({ piece, vel: vel.clone(), spin: spin.clone(), life: 1.2 });
	}
	function flyStep(dt) {
		st.flying = st.flying.filter((f) => {
			f.life -= dt;
			f.vel.y -= dt * 6;
			f.piece.position.addScaledVector(f.vel, dt);
			f.piece.rotation.x += f.spin.x * dt; f.piece.rotation.y += f.spin.y * dt; f.piece.rotation.z += f.spin.z * dt;
			f.piece.traverse((m) => { if (m.material) m.material.opacity = Math.max(0, f.life / 1.2); });
			if (f.life > 0) return true;
			scene.remove(f.piece);
			f.piece.traverse((m) => m.material?.dispose());
			return false;
		});
	}

	// la censura: el personaje entero se pixela (como el mosaico de la tele) y se ve borroso lo que hace detrás
	let censored = false;
	function censor(on) {
		censored = on;
		renderer.domElement.classList.toggle("censored", on);
		renderer.domElement.style.imageRendering = on ? "pixelated" : "";
		if (!on) { renderer.setPixelRatio(basePR); renderer.setSize(...size(), false); }
	}
	function censorBlocks(t) { // el tamaño del bloque cambia a saltos: parece que el mosaico "respira"
		const pr = [0.09, 0.12, 0.075, 0.11][Math.floor(t * 9) % 4];
		if (renderer.getPixelRatio() !== pr) { renderer.setPixelRatio(pr); renderer.setSize(...size(), false); }
	}

	// ---------- eventos ----------
	const onPointer = (e) => {
		const r = container.getBoundingClientRect();
		const cx = r.left + r.width / 2, cy = r.top + r.height * 0.35;
		const dx = e.clientX - cx, dy = e.clientY - cy;
		st.lookTarget.set(THREE.MathUtils.clamp(dx / 320, -1, 1), THREE.MathUtils.clamp(dy / 260, -1, 1));
		st.near = THREE.MathUtils.clamp(1 - Math.hypot(dx, dy) / 260, 0, 1);
		if (st.asleep) { st.asleep = false; show("surprised", 1.0); gesture("jump"); }
		st.idleFor = 0;
	};
	const onEnter = () => { st.hover = 1; if (!st.asleep) show("curious", 0.9); };
	const onLeave = () => { st.hover = 0; };
	const onClick = () => {
		const r = ["wave", "jump", "spinJoy", "love", "laugh"][Math.floor(Math.random() * 5)];
		if (r === "love" || r === "laugh") { show(r, 1.4); gesture("jump"); } else { gesture(r); show("happy", 1.2); }
	};
	window.addEventListener("pointermove", onPointer, { passive: true });
	container.parentElement?.addEventListener("pointerenter", onEnter);
	container.parentElement?.addEventListener("pointerleave", onLeave);
	container.parentElement?.addEventListener("click", onClick);
	const onVisibility = () => setRunning(!document.hidden && st.visible);
	document.addEventListener("visibilitychange", onVisibility);
	const ro = new ResizeObserver(() => { const [w, h] = size(); renderer.setSize(w, h); camera.aspect = w / h; camera.updateProjectionMatrix(); renderOnce(); });
	ro.observe(container);

	function renderOnce() { step(0); renderer.render(scene, camera); }
	function setRunning(on) {
		on = on && !reducedMotion;
		if (on === st.running) return;
		st.running = on;
		st.last = 0;
		if (on) requestAnimationFrame(frame);
	}

	applyOutfit(st.outfit);
	gesture("wave"); // llega saludando
	renderOnce();
	setRunning(true);

	return {
		/** idle | thinking | success | error */
		setMood(mood) {
			if (mood === st.mood) return;
			st.mood = mood;
			st.asleep = false;
			st.idleFor = 0;
			if (mood === "success") { gesture(Math.random() < 0.5 ? "thumbs" : "jump"); show("stars", 1.6); }
			if (mood === "error") { st.shake = 1; gesture("facepalm"); }
			if (mood === "thinking") gesture(Math.random() < 0.5 ? "scratch" : "chin");
			if (reducedMotion) renderOnce();
		},
		/** Cambia la ropa con un giro y chispas (sin animación, al instante). */
		setTheme(name) {
			const o = OUTFITS[name];
			if (!o || o === st.outfit) return;
			st.outfit = o;
			if (reducedMotion || st.change) { applyOutfit(o); if (st.change) st.change.to = o; renderOnce(); return; }
			st.change = { t: 0, to: o };
			onEvent("changeStart");
			st.asleep = false;
			st.idleFor = 0;
			show("surprised", 0.9);
		},
		/** Habla: mueve la boca un rato (cuando aparece una respuesta). */
		talk(secs = 1.6) { st.talk = Math.min(4, secs); st.asleep = false; st.idleFor = 0; },
		/** Asiente (cuando le hacen una pregunta). */
		nod() { st.asleep = false; st.idleFor = 0; gesture("nod"); show("curious", 0.9); },
		wave() { gesture("wave"); },
		/** Un gesto con su expresión (para acompañar lo que dice): act("shush", "shush"). */
		act(name, faceName, secs = 1.8) {
			st.asleep = false;
			st.idleFor = 0;
			gesture(name);
			if (faceName) show(faceName, secs);
		},
		/** Con el panel abierto mira hacia la conversación. */
		setPanel(open) { st.panel = open; st.idleFor = 0; st.asleep = false; if (open) gesture("wave"); },
		setVisible(v) { st.visible = v; setRunning(v && !document.hidden); },
		destroy() {
			setRunning(false);
			window.removeEventListener("pointermove", onPointer);
			document.removeEventListener("visibilitychange", onVisibility);
			ro.disconnect();
			renderer.dispose();
			renderer.domElement.remove();
		},
	};
}

function roundRect(ctx, x, y, w, h, r) {
	r = Math.max(0, Math.min(r, Math.abs(w) / 2, Math.abs(h) / 2));
	ctx.beginPath();
	ctx.moveTo(x + r, y);
	ctx.arcTo(x + w, y, x + w, y + h, r);
	ctx.arcTo(x + w, y + h, x, y + h, r);
	ctx.arcTo(x, y + h, x, y, r);
	ctx.arcTo(x, y, x + w, y, r);
	ctx.closePath();
}
function star(ctx, x, y, R, r) {
	ctx.beginPath();
	for (let i = 0; i < 10; i++) {
		const a = -Math.PI / 2 + (i * Math.PI) / 5, rad = i % 2 ? r : R;
		ctx.lineTo(x + Math.cos(a) * rad, y + Math.sin(a) * rad);
	}
	ctx.closePath();
}
function heart(ctx, x, y, s) {
	ctx.beginPath();
	ctx.moveTo(x, y + s * 0.75);
	ctx.bezierCurveTo(x - s * 1.2, y, x - s * 0.6, y - s * 0.95, x, y - s * 0.35);
	ctx.bezierCurveTo(x + s * 0.6, y - s * 0.95, x + s * 1.2, y, x, y + s * 0.75);
	ctx.closePath();
}

// un texto luminoso (el </> del pecho y la capucha, la z de dormir) en una textura
function textTexture(text, w, h) {
	const c = document.createElement("canvas");
	c.width = w;
	c.height = h;
	const tex = new THREE.CanvasTexture(c);
	tex.colorSpace = THREE.SRGBColorSpace;
	tex.userData = { text, canvas: c };
	recolorText(tex, 0x29dfff);
	return tex;
}
function recolorText(tex, hex) {
	const { text, canvas } = tex.userData;
	const ctx = canvas.getContext("2d");
	const color = `#${hex.toString(16).padStart(6, "0")}`;
	ctx.clearRect(0, 0, canvas.width, canvas.height);
	ctx.font = `800 ${Math.round(canvas.height * 0.72)}px "JetBrains Mono", Consolas, monospace`;
	ctx.textAlign = "center";
	ctx.textBaseline = "middle";
	ctx.fillStyle = color;
	ctx.shadowColor = color;
	ctx.shadowBlur = 10;
	ctx.fillText(text, canvas.width / 2, canvas.height / 2 + 2);
	tex.needsUpdate = true;
}
